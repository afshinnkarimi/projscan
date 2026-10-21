package scanner

import (
	"bufio"

	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"strings"
)

var helmVersionPattern = regexp.MustCompile(`^([vV]?[0-9]+\.[0-9]+)(?:\.([0-9]+))?(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func inventoryForHelmArchive(contents archiveContents) ArtifactInventory {
	inventory := ArtifactInventory{
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

func scanHelmDirectory(chartPath string) Result {
	result := Result{
		Name: filepath.Base(chartPath),
		Path: chartPath,
		Type: "helm-chart",
		Inventory: ArtifactInventory{
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

type chartVersion struct {
	name    string
	version string
}

func helmChartVersions(inventory ArtifactInventory) []chartVersion {
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
