package releases

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var releaseNamePattern = regexp.MustCompile(`^[A-Za-z]+(?:_[A-Za-z]+)*_[0-9]+\.[0-9]+\.[0-9]+-[0-9]+$`)
var releaseVersionPattern = regexp.MustCompile(`^(.+)_([0-9]+)\.([0-9]+)\.([0-9]+)-([0-9]+)$`)

type Result struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func LatestInDirectory(root string) ([]Result, error) {
	var found []Result
	if err := fs.WalkDir(os.DirFS(root), ".", func(relativePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && relativePath != "." &&
			releaseNamePattern.MatchString(entry.Name()) &&
			isServerSidePath(relativePath) {
			found = append(found, Result{
				Name: entry.Name(),
				Path: filepath.Join(root, relativePath),
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return Latest(found), nil
}

func Latest(results []Result) []Result {
	type candidate struct {
		result  Result
		version []int
	}

	latest := make(map[string]candidate)
	for _, result := range results {
		base, version, ok := parseVersion(result.Name)
		if !ok {
			continue
		}
		current, exists := latest[base]
		if !exists || compare(version, current.version) > 0 {
			latest[base] = candidate{result: result, version: version}
		}
	}

	selected := make([]Result, 0, len(latest))
	for _, item := range latest {
		selected = append(selected, item.result)
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Name < selected[j].Name
	})
	return selected
}

func parseVersion(name string) (string, []int, bool) {
	if !releaseNamePattern.MatchString(name) {
		return "", nil, false
	}
	matches := releaseVersionPattern.FindStringSubmatch(name)
	if len(matches) != 6 {
		return "", nil, false
	}
	version := make([]int, 3)
	for i, part := range matches[2:5] {
		value, err := strconv.Atoi(part)
		if err != nil {
			return "", nil, false
		}
		version[i] = value
	}
	return matches[1], version, true
}

func compare(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

func isServerSidePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "ServerSide" {
			return true
		}
	}
	return false
}
