package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ScanArtifacts(root string) ([]Result, error) {
	var results []Result
	err := filepath.WalkDir(root, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			chartFile := filepath.Join(currentPath, "Chart.yaml")
			if _, err := os.Stat(chartFile); err == nil {
				results = append(results, scanHelmDirectory(currentPath))
				return filepath.SkipDir
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		if entry.Type().IsRegular() && isTarArchiveName(entry.Name()) {
			results = append(results, scanTarArchive(currentPath))
		}
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

func scanTarArchive(filePath string) Result {
	result := Result{
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
			details := &DockerImageDetails{}
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
