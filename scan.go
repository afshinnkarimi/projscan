package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxMetadataSize = 4 << 20
const maxJarSize = 64 << 20

type artifactInventory struct {
	Files        []string            `json:"files,omitempty"`
	Dependencies []string            `json:"dependencies,omitempty"`
	Metadata     map[string]string   `json:"metadata,omitempty"`
	Image        *dockerImageDetails `json:"image,omitempty"`
}

type dockerImageDetails struct {
	Config []string `json:"config"`
	Tags   []string `json:"tags,omitempty"`
	Layers []string `json:"layers"`
}

type dockerManifestEntry struct {
	Config   string   `json:"Config"`
	RepoTags []string `json:"RepoTags"`
	Layers   []string `json:"Layers"`
}

type archiveContents struct {
	files              []string
	manifestFound      bool
	manifest           []byte
	manifestReadErr    error
	chartFiles         map[string][]byte
	javaFiles          map[string][]byte
	javaApplications   []javaApplication
	javaIndicatorsSeen bool
	errs               []string
}

type javaApplication struct {
	Name      string
	Version   string
	MainClass bool
}

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

var semanticVersionPattern = regexp.MustCompile(`^[vV]?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)
var helmVersionPattern = regexp.MustCompile(`^([vV]?[0-9]+\.[0-9]+)(?:\.([0-9]+))?(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
var javaArchiveVersionPattern = regexp.MustCompile(`^(.+)[-_]([vV]?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)
var gradleDependencyPattern = regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testRuntimeOnly)\s*(?:\(\s*)?["']([^"' ]+:[^"' ]+:[^"' ]+)["']`)

func scanArtifacts(root string) ([]scanResult, error) {
	var results []scanResult
	err := filepath.WalkDir(root, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			chartPath := filepath.Join(currentPath, "Chart.yaml")
			_, statErr := os.Stat(chartPath)
			if statErr == nil {
				results = append(results, scanHelmDirectory(currentPath))
				return filepath.SkipDir
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
			return nil
		}
		if !entry.Type().IsRegular() || !isTarArchiveName(entry.Name()) {
			return nil
		}
		results = append(results, scanTarArchive(currentPath))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})
	return results, nil
}

func isTarArchiveName(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".tar") ||
		strings.HasSuffix(name, ".tar.gz") ||
		strings.HasSuffix(name, ".tgz")
}

func scanTarArchive(filePath string) scanResult {
	result := scanResult{
		Name: filepath.Base(filePath),
		Path: filePath,
		Type: "unknown",
	}
	contents, err := readTarArchive(filePath)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	result.Errors = append(result.Errors, contents.errs...)

	switch {
	case contents.manifestFound:
		result.Type = "docker-image"
		var manifest []dockerManifestEntry
		if contents.manifestReadErr != nil {
			result.Errors = append(result.Errors, contents.manifestReadErr.Error())
		} else if err := json.Unmarshal(contents.manifest, &manifest); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("invalid Docker manifest.json: %v", err))
		} else if len(manifest) == 0 {
			result.Errors = append(result.Errors, "Docker manifest.json contains no image entries")
		} else {
			details := &dockerImageDetails{}
			files := make(map[string]struct{}, len(contents.files))
			for _, file := range contents.files {
				files[file] = struct{}{}
			}
			for _, image := range manifest {
				if image.Config == "" || len(image.Layers) == 0 {
					result.Errors = append(result.Errors, "Docker manifest entry is missing its config or layers")
				}
				for _, reference := range append([]string{image.Config}, image.Layers...) {
					if _, exists := files[cleanArchivePath(reference)]; !exists {
						result.Errors = append(result.Errors, fmt.Sprintf("Docker manifest references missing file %q", reference))
					}
				}
				details.Config = append(details.Config, image.Config)
				details.Tags = append(details.Tags, image.RepoTags...)
				details.Layers = append(details.Layers, image.Layers...)
			}
			details.Tags = sortedUnique(details.Tags)
			details.Config = sortedUnique(details.Config)
			details.Layers = sortedUnique(details.Layers)
			result.Inventory.Image = details
			result.Inventory.Files = append([]string(nil), contents.files...)
		}
	case len(contents.chartFiles) > 0:
		result.Type = "helm-chart"
		result.Inventory = inventoryForHelmArchive(contents)
	case contents.javaIndicatorsSeen:
		result.Type = "java-application"
		var scanErrors []string
		result.Inventory, scanErrors = inventoryForJavaArchive(contents)
		result.Errors = append(result.Errors, scanErrors...)
		if result.Inventory.Metadata == nil {
			result.Inventory.Metadata = make(map[string]string)
		}
		result.Inventory.Metadata["containerized"] = "false"
		if appName, appVersion, ok := parseJavaArchiveName(result.Name); ok {
			result.latestKey = "java:" + strings.ToLower(appName)
			result.latestTag = appVersion
		}
	default:
		result.Errors = append(result.Errors, "archive does not contain a Docker image, Helm chart, or Java application")
	}
	result.Errors = sortedUnique(result.Errors)
	return result
}

func readTarArchive(filePath string) (archiveContents, error) {
	contents := archiveContents{
		chartFiles: make(map[string][]byte),
		javaFiles:  make(map[string][]byte),
	}
	file, err := os.Open(filePath)
	if err != nil {
		return contents, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var archiveReader io.Reader = reader
	header, peekErr := reader.Peek(2)
	if peekErr == nil && header[0] == 0x1f && header[1] == 0x8b {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return contents, fmt.Errorf("open gzip archive: %w", err)
		}
		defer gzipReader.Close()
		archiveReader = gzipReader
	}

	tarReader := tar.NewReader(archiveReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return contents, fmt.Errorf("read tar archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		fileName := cleanArchivePath(header.Name)
		if fileName == "" {
			continue
		}
		contents.files = append(contents.files, fileName)

		baseName := path.Base(fileName)
		if baseName == "manifest.json" {
			contents.manifestFound = true
			contents.manifest, contents.manifestReadErr = readArchiveMetadata(tarReader, header.Size)
			continue
		}
		if baseName == "Chart.yaml" {
			data, readErr := readArchiveMetadata(tarReader, header.Size)
			if readErr != nil {
				contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
			} else {
				contents.chartFiles[fileName] = data
			}
			continue
		}
		if isJavaBuildFile(fileName) {
			contents.javaIndicatorsSeen = true
			data, readErr := readArchiveMetadata(tarReader, header.Size)
			if readErr != nil {
				contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
			} else {
				contents.javaFiles[fileName] = data
			}
			continue
		}
		if isJavaBinary(fileName) {
			contents.javaIndicatorsSeen = true
			if strings.EqualFold(path.Ext(fileName), ".jar") {
				if header.Size > maxJarSize {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: JAR exceeds %d bytes", fileName, maxJarSize))
					continue
				}
				data, readErr := io.ReadAll(io.LimitReader(tarReader, maxJarSize+1))
				if readErr != nil {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: %v", fileName, readErr))
					continue
				}
				if int64(len(data)) > maxJarSize {
					contents.errs = append(contents.errs, fmt.Sprintf("read %s: JAR exceeds %d bytes", fileName, maxJarSize))
					continue
				}
				app, manifestErr := javaApplicationFromJar(fileName, data)
				if manifestErr != nil {
					contents.errs = append(contents.errs, fmt.Sprintf("read JAR manifest %s: %v", fileName, manifestErr))
				} else if app != nil {
					contents.javaApplications = append(contents.javaApplications, *app)
				}
			}
		}
	}
	contents.files = sortedUnique(contents.files)
	return contents, nil
}

func readArchiveMetadata(reader io.Reader, size int64) ([]byte, error) {
	if size > maxMetadataSize {
		return nil, fmt.Errorf("metadata file exceeds %d bytes", maxMetadataSize)
	}
	return io.ReadAll(io.LimitReader(reader, maxMetadataSize+1))
}

func cleanArchivePath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}
	return cleaned
}

func inventoryForHelmArchive(contents archiveContents) artifactInventory {
	inventory := artifactInventory{
		Files:    append([]string(nil), contents.files...),
		Metadata: make(map[string]string),
	}
	for chartPath, data := range contents.chartFiles {
		metadata := parseChartMetadata(data)
		prefix := strings.TrimSuffix(chartPath, "Chart.yaml")
		for key, value := range metadata {
			inventory.Metadata[prefix+key] = value
		}
	}
	if len(inventory.Metadata) == 0 {
		inventory.Metadata = nil
	}
	return inventory
}

func scanHelmDirectory(chartPath string) scanResult {
	result := scanResult{
		Name: filepath.Base(chartPath),
		Path: chartPath,
		Type: "helm-chart",
		Inventory: artifactInventory{
			Metadata: make(map[string]string),
		},
	}
	chartFile := filepath.Join(chartPath, "Chart.yaml")
	data, err := os.ReadFile(chartFile)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("read Helm chart metadata %s: %v", chartFile, err))
	} else {
		result.Inventory.Metadata = parseChartMetadata(data)
		result.latestKey = "helm:" + result.Inventory.Metadata["name"]
		result.latestTag = result.Inventory.Metadata["version"]
	}

	err = filepath.WalkDir(chartPath, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(chartPath, currentPath)
		if err != nil {
			return err
		}
		result.Inventory.Files = append(result.Inventory.Files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	sort.Strings(result.Inventory.Files)
	result.Errors = sortedUnique(result.Errors)
	return result
}

func parseChartMetadata(data []byte) map[string]string {
	metadata := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch key {
		case "apiVersion", "name", "version", "appVersion", "type", "description":
			value = strings.TrimSpace(strings.SplitN(value, "#", 2)[0])
			value = strings.Trim(value, `"'`)
			if value != "" {
				metadata[key] = value
			}
		}
	}
	return metadata
}

func inventoryForJavaArchive(contents archiveContents) (artifactInventory, []string) {
	inventory := artifactInventory{
		Files:        append([]string(nil), contents.files...),
		Dependencies: make([]string, 0),
	}
	var scanErrors []string
	for fileName, data := range contents.javaFiles {
		switch {
		case strings.EqualFold(path.Base(fileName), "pom.xml"):
			dependencies, err := mavenDependencies(data)
			if err != nil {
				scanErrors = append(scanErrors, fmt.Sprintf("parse %s: %v", fileName, err))
				continue
			}
			inventory.Dependencies = append(inventory.Dependencies, dependencies...)
		case isGradleBuildFile(fileName):
			inventory.Dependencies = append(inventory.Dependencies, gradleDependencies(data)...)
		}
	}
	inventory.Dependencies = sortedUnique(inventory.Dependencies)
	if len(inventory.Dependencies) == 0 {
		inventory.Dependencies = nil
	}
	if app := primaryJavaApplication(contents.javaApplications); app != nil {
		inventory.Metadata = map[string]string{
			"application": app.Name,
			"version":     app.Version,
		}
	}
	return inventory, scanErrors
}

func primaryJavaApplication(applications []javaApplication) *javaApplication {
	if len(applications) == 0 {
		return nil
	}
	sort.Slice(applications, func(i, j int) bool {
		if applications[i].MainClass != applications[j].MainClass {
			return applications[i].MainClass
		}
		if applications[i].Name != applications[j].Name {
			return applications[i].Name < applications[j].Name
		}
		return applications[i].Version < applications[j].Version
	})
	return &applications[0]
}

func javaApplicationFromJar(fileName string, data []byte) (*javaApplication, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if !strings.EqualFold(file.Name, "META-INF/MANIFEST.MF") {
			continue
		}
		if file.UncompressedSize64 > maxMetadataSize {
			return nil, fmt.Errorf("manifest exceeds %d bytes", maxMetadataSize)
		}
		manifestFile, err := file.Open()
		if err != nil {
			return nil, err
		}
		manifest, readErr := io.ReadAll(io.LimitReader(manifestFile, maxMetadataSize+1))
		closeErr := manifestFile.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		attributes := parseJavaManifest(manifest)
		name := attributes["implementation-title"]
		if name == "" {
			name = strings.TrimSpace(strings.SplitN(attributes["bundle-symbolicname"], ";", 2)[0])
		}
		if name == "" {
			name = strings.TrimSuffix(path.Base(fileName), path.Ext(fileName))
		}
		version := attributes["implementation-version"]
		if version == "" {
			version = attributes["bundle-version"]
		}
		if version == "" {
			version = attributes["specification-version"]
		}
		return &javaApplication{
			Name:      name,
			Version:   version,
			MainClass: attributes["main-class"] != "",
		}, nil
	}
	return nil, nil
}

func parseJavaManifest(data []byte) map[string]string {
	attributes := make(map[string]string)
	var previousKey string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, " ") && previousKey != "" {
			attributes[previousKey] += strings.TrimPrefix(line, " ")
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			previousKey = ""
			continue
		}
		key = strings.ToLower(key)
		previousKey = key
		attributes[key] = strings.TrimSpace(value)
	}
	return attributes
}

func isJavaBuildFile(fileName string) bool {
	base := strings.ToLower(path.Base(fileName))
	return base == "pom.xml" || isGradleBuildFile(fileName)
}

func isGradleBuildFile(fileName string) bool {
	base := strings.ToLower(path.Base(fileName))
	return base == "build.gradle" || base == "build.gradle.kts"
}

func isJavaBinary(fileName string) bool {
	switch strings.ToLower(path.Ext(fileName)) {
	case ".jar", ".war", ".ear", ".class":
		return true
	default:
		return false
	}
}

func mavenDependencies(data []byte) ([]string, error) {
	type dependency struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	}
	var project struct {
		Dependencies []dependency `xml:"dependencies>dependency"`
	}
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(project.Dependencies))
	for _, dependency := range project.Dependencies {
		if dependency.GroupID == "" || dependency.ArtifactID == "" {
			continue
		}
		name := dependency.GroupID + ":" + dependency.ArtifactID
		if dependency.Version != "" {
			name += ":" + dependency.Version
		}
		result = append(result, name)
	}
	return result, nil
}

func gradleDependencies(data []byte) []string {
	matches := gradleDependencyPattern.FindAllSubmatch(data, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, string(match[1]))
	}
	return result
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type artifactVersionCandidate struct {
	index   int
	group   string
	version semanticVersion
}

func latestArtifacts(results []scanResult) []scanResult {
	latest := make(map[string]semanticVersion)
	var candidates []artifactVersionCandidate
	hasVersionCandidate := make([]bool, len(results))
	dockerTags := make([][]dockerVersionTag, len(results))
	versionedDockerRepositories := make(map[string]struct{})
	latestHelmResult := make(map[string]int)
	versionedHelmGroups := make(map[string]struct{})
	unversionedHelmResult := make(map[string]int)

	addCandidate := func(index int, group, tag string) bool {
		version, ok := parseSemanticVersion(tag)
		if !ok || group == "" {
			return false
		}
		candidates = append(candidates, artifactVersionCandidate{
			index: index, group: group, version: version,
		})
		hasVersionCandidate[index] = true
		if current, exists := latest[group]; !exists || compareSemanticVersions(version, current) > 0 {
			latest[group] = version
		}
		return true
	}

	for i, result := range results {
		switch result.Type {
		case "docker-image":
			if result.Inventory.Image == nil || len(result.Inventory.Image.Tags) == 0 {
				continue
			}
			for _, tag := range result.Inventory.Image.Tags {
				repository, versionTag, ok := parseDockerTag(tag)
				if !ok {
					dockerTags[i] = append(dockerTags[i], dockerVersionTag{tag: tag})
					continue
				}
				group := "docker:" + repository
				version, versioned := parseSemanticVersion(versionTag)
				if versioned {
					versionedDockerRepositories[group] = struct{}{}
					addCandidate(i, group, versionTag)
				}
				dockerTags[i] = append(dockerTags[i], dockerVersionTag{
					tag: tag, group: group, version: version, versioned: versioned,
				})
			}
		case "helm-chart":
			for _, chart := range helmChartVersions(result.Inventory) {
				group := "helm:" + strings.ToLower(chart.name)
				normalizedVersion, ok := normalizeHelmVersion(chart.version)
				if !ok {
					winner, hasWinner := unversionedHelmResult[group]
					if !hasWinner || results[i].Path < results[winner].Path {
						unversionedHelmResult[group] = i
					}
					continue
				}
				version, _ := parseSemanticVersion(normalizedVersion)
				versionedHelmGroups[group] = struct{}{}
				current, exists := latest[group]
				addCandidate(i, group, normalizedVersion)
				switch {
				case !exists, compareSemanticVersions(version, current) > 0:
					latestHelmResult[group] = i
				case compareSemanticVersions(version, current) == 0:
					winner, hasWinner := latestHelmResult[group]
					if !hasWinner || results[i].Path < results[winner].Path {
						latestHelmResult[group] = i
					}
				}
			}
		case "java-application":
			if result.latestKey != "" {
				addCandidate(i, result.latestKey, result.latestTag)
			}
		}
	}

	selected := make([]bool, len(results))
	for _, candidate := range candidates {
		if compareSemanticVersions(candidate.version, latest[candidate.group]) == 0 {
			selected[candidate.index] = true
		}
	}

	for i, result := range results {
		switch result.Type {
		case "docker-image":
			if result.Inventory.Image == nil || len(result.Inventory.Image.Tags) == 0 {
				selected[i] = true
				break
			}
			keptTags := make([]string, 0, len(dockerTags[i]))
			hasVersionedTag := false
			for _, tag := range dockerTags[i] {
				if tag.versioned {
					hasVersionedTag = true
					if compareSemanticVersions(tag.version, latest[tag.group]) == 0 {
						keptTags = append(keptTags, tag.tag)
					}
					continue
				}
				if _, hasVersionedRepository := versionedDockerRepositories[tag.group]; !hasVersionedRepository {
					keptTags = append(keptTags, tag.tag)
					continue
				}
			}
			if len(keptTags) > 0 && (!hasVersionedTag || selected[i]) {
				selected[i] = true
				result.Inventory.Image.Tags = sortedUnique(keptTags)
			} else {
				selected[i] = false
			}
		case "helm-chart":
			selected[i] = false
		case "java-application":
			if !hasVersionCandidate[i] {
				selected[i] = true
			}
		default:
			selected[i] = true
		}
	}
	for _, index := range latestHelmResult {
		selected[index] = true
	}
	for group, index := range unversionedHelmResult {
		if _, hasComparableVersion := versionedHelmGroups[group]; !hasComparableVersion {
			selected[index] = true
		}
	}

	filtered := make([]scanResult, 0, len(results))
	for i, result := range results {
		if selected[i] {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

type dockerVersionTag struct {
	tag       string
	group     string
	version   semanticVersion
	versioned bool
}

func parseJavaArchiveName(fileName string) (string, string, bool) {
	name := filepath.Base(fileName)
	lowerName := strings.ToLower(name)
	for _, extension := range []string{".tar.gz", ".tgz", ".tar"} {
		if strings.HasSuffix(lowerName, extension) {
			name = name[:len(name)-len(extension)]
			break
		}
	}
	matches := javaArchiveVersionPattern.FindStringSubmatch(name)
	if len(matches) != 3 || matches[1] == "" {
		return "", "", false
	}
	if _, ok := parseSemanticVersion(matches[2]); !ok {
		return "", "", false
	}
	return strings.TrimRight(matches[1], "-_"), matches[2], true
}

type chartVersion struct {
	name    string
	version string
}

func helmChartVersions(inventory artifactInventory) []chartVersion {
	var charts []chartVersion
	rootDepth := -1
	for key := range inventory.Metadata {
		if key != "name" && !strings.HasSuffix(key, "/name") {
			continue
		}
		depth := strings.Count(key, "/")
		if rootDepth == -1 || depth < rootDepth {
			rootDepth = depth
		}
	}
	for key, name := range inventory.Metadata {
		if key != "name" && !strings.HasSuffix(key, "/name") {
			continue
		}
		if strings.Count(key, "/") != rootDepth {
			continue
		}
		prefix := strings.TrimSuffix(key, "name")
		version := inventory.Metadata[prefix+"version"]
		if name != "" && version != "" {
			charts = append(charts, chartVersion{name: name, version: version})
		}
	}
	sort.Slice(charts, func(i, j int) bool {
		if charts[i].name != charts[j].name {
			return charts[i].name < charts[j].name
		}
		return charts[i].version < charts[j].version
	})
	return charts
}

func normalizeHelmVersion(version string) (string, bool) {
	version = strings.Trim(strings.TrimSpace(version), `"'`)
	matches := helmVersionPattern.FindStringSubmatch(version)
	if len(matches) != 5 {
		return "", false
	}
	if matches[2] == "" {
		version = matches[1] + ".0" + matches[3] + matches[4]
	}
	if _, ok := parseSemanticVersion(version); !ok {
		return "", false
	}
	return version, true
}

func parseDockerTag(reference string) (string, string, bool) {
	if strings.Contains(reference, "@") {
		return "", "", false
	}
	colon := strings.LastIndexByte(reference, ':')
	slash := strings.LastIndexByte(reference, '/')
	if colon <= slash || colon == len(reference)-1 {
		return "", "", false
	}
	repository, tag := reference[:colon], reference[colon+1:]
	if repository == "" {
		return "", "", false
	}
	return strings.ToLower(repository), tag, true
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	matches := semanticVersionPattern.FindStringSubmatch(value)
	if len(matches) != 5 {
		return semanticVersion{}, false
	}
	for _, part := range matches[1:4] {
		if len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, false
		}
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return semanticVersion{}, false
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return semanticVersion{}, false
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return semanticVersion{}, false
	}
	var prerelease []string
	if matches[4] != "" {
		prerelease = strings.Split(matches[4], ".")
		for _, identifier := range prerelease {
			if identifier == "" {
				return semanticVersion{}, false
			}
			if _, err := strconv.Atoi(identifier); err == nil && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, false
			}
		}
	}
	return semanticVersion{major: major, minor: minor, patch: patch, prerelease: prerelease}, true
}

func compareSemanticVersions(a, b semanticVersion) int {
	if a.major != b.major {
		if a.major > b.major {
			return 1
		}
		return -1
	}
	if a.minor != b.minor {
		if a.minor > b.minor {
			return 1
		}
		return -1
	}
	if a.patch != b.patch {
		if a.patch > b.patch {
			return 1
		}
		return -1
	}
	if len(a.prerelease) == 0 && len(b.prerelease) == 0 {
		return 0
	}
	if len(a.prerelease) == 0 {
		return 1
	}
	if len(b.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		left, right := a.prerelease[i], b.prerelease[i]
		leftNumber, leftErr := strconv.Atoi(left)
		rightNumber, rightErr := strconv.Atoi(right)
		switch {
		case leftErr == nil && rightErr == nil && leftNumber != rightNumber:
			if leftNumber > rightNumber {
				return 1
			}
			return -1
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case left != right:
			if left > right {
				return 1
			}
			return -1
		}
	}
	if len(a.prerelease) > len(b.prerelease) {
		return 1
	}
	if len(a.prerelease) < len(b.prerelease) {
		return -1
	}
	return 0
}
