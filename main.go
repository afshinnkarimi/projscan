package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var releaseNamePattern = regexp.MustCompile(`^[A-Za-z]+(?:_[A-Za-z]+)*_[0-9]+\.[0-9]+\.[0-9]+-[0-9]+$`)

type directoryResult struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func isReleaseName(name string) bool {
	return releaseNamePattern.MatchString(name)
}

func isServerSidePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "ServerSide" {
			return true
		}
	}
	return false
}

func listDirectories(dir string) []string {
	results := listDirectoryResults(dir)
	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.Name)
	}
	return names
}

func listDirectoryResults(dir string) []directoryResult {
	var results []directoryResult

	_ = fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == "." {
			return nil
		}
		if isReleaseName(d.Name()) && isServerSidePath(path) {
			results = append(results, directoryResult{
				Name: d.Name(),
				Path: filepath.Join(dir, path),
			})
		}
		return nil
	})

	return results
}

func latestVersionResults(results []directoryResult) []directoryResult {
	latest := make(map[string]directoryResult)
	latestVersion := make(map[string][]int)

	for _, result := range results {
		if !isReleaseName(result.Name) {
			continue
		}

		base, version, ok := parseReleaseName(result.Name)
		if !ok {
			continue
		}

		if _, exists := latest[base]; !exists || compareVersions(version, latestVersion[base]) > 0 {
			latest[base] = result
			latestVersion[base] = version
		}
	}

	selected := make([]directoryResult, 0, len(latest))
	for _, result := range latest {
		selected = append(selected, result)
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Name < selected[j].Name
	})
	return selected
}

func listDirectoryNames(results []directoryResult) []string {
	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.Name)
	}
	return names
}

func latestVersionNames(names []string) []string {
	latest := make(map[string]string)
	latestVersion := make(map[string][]int)

	for _, name := range names {
		if !isReleaseName(name) {
			continue
		}

		base, version, ok := parseReleaseName(name)
		if !ok {
			continue
		}

		if current, exists := latest[base]; !exists || compareVersions(version, latestVersion[base]) > 0 {
			latest[base] = name
			latestVersion[base] = version
			_ = current
		}
	}

	result := make([]string, 0, len(latest))
	for _, name := range latest {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func parseReleaseName(name string) (string, []int, bool) {
	matches := regexp.MustCompile(`^(.+)_([0-9]+)\.([0-9]+)\.([0-9]+)-([0-9]+)$`).FindStringSubmatch(name)
	if len(matches) != 6 {
		return "", nil, false
	}

	base := matches[1]
	versionNums := []string{matches[2], matches[3], matches[4]}
	version := make([]int, 0, len(versionNums))
	for _, part := range versionNums {
		v, err := strconv.Atoi(part)
		if err != nil {
			return "", nil, false
		}
		version = append(version, v)
	}
	return base, version, true
}

func compareVersions(a, b []int) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func main() {
	directory, _ := os.Getwd()
	searchDir := directory

	results := latestVersionResults(listDirectoryResults(searchDir))
	for _, item := range listDirectoryNames(results) {
		fmt.Println(item)
	}

	jsonPath := filepath.Join(directory, "results.json")
	payload, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode results: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(jsonPath, payload, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", jsonPath, err)
		os.Exit(1)
	}
	fmt.Printf("\nSaved JSON results to: %s\n", jsonPath)
}
