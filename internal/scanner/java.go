package scanner

import (
	"archive/zip"

	"bytes"

	"encoding/xml"

	"fmt"

	"io"

	"path"

	"regexp"

	"sort"

	"strings"
)

type javaApplication struct {
	Name      string
	Version   string
	MainClass bool
}

var gradleDependencyPattern = regexp.MustCompile(`(?:implementation|api|compile|runtimeOnly|testImplementation|testRuntimeOnly)\s*(?:\(\s*)?["']([^"' ]+:[^"' ]+:[^"' ]+)["']`)

func inventoryForJavaArchive(contents archiveContents) (ArtifactInventory, []string) {
	inventory := ArtifactInventory{
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
