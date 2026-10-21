package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var releaseNamePattern = regexp.MustCompile(`^[A-Za-z]+(?:_[A-Za-z]+)*_[0-9]+\.[0-9]+\.[0-9]+-[0-9]+$`)

type directoryResult struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type scanResult struct {
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	Type      string            `json:"type"`
	Inventory artifactInventory `json:"inventory,omitempty"`
	Errors    []string          `json:"errors,omitempty"`
	latestKey string
	latestTag string
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command := newRootCommand(stdout, stderr)
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	config := viper.New()
	config.SetEnvPrefix("PROJSCAN")
	config.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	config.AutomaticEnv()

	command := &cobra.Command{
		Use:           "projscan",
		Short:         "Scan artifacts in a directory",
		Long:          "Scan Docker image archives, Helm charts, Java application archives, and release directories.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			directory, err := filepath.Abs(config.GetString("directory"))
			if err != nil {
				return fmt.Errorf("resolve scan directory: %w", err)
			}
			return scanDirectory(directory, config.GetBool("latest"), cmd.OutOrStdout())
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := config.BindPFlag("directory", cmd.Flags().Lookup("directory")); err != nil {
				return fmt.Errorf("bind directory flag: %w", err)
			}
			if err := config.BindPFlag("latest", cmd.Flags().Lookup("latest")); err != nil {
				return fmt.Errorf("bind latest flag: %w", err)
			}
			if cmd.Flags().Changed("dir") {
				config.Set("directory", cmd.Flag("dir").Value.String())
			}
			return nil
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.Flags().StringP("directory", "d", ".", "directory to scan")
	command.Flags().String("dir", "", "alias for --directory")
	command.Flags().Bool("latest", false, "keep only latest Docker tags, Helm chart versions, and Java archive filename versions")
	return command
}

func scanDirectory(directory string, latestOnly bool, stdout io.Writer) error {
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("access scan directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan target is not a directory: %s", directory)
	}

	releases := latestVersionResults(listDirectoryResults(directory))
	for _, item := range listDirectoryNames(releases) {
		fmt.Fprintln(stdout, item)
	}

	results := make([]scanResult, 0, len(releases))
	for _, release := range releases {
		results = append(results, scanResult{
			Name: release.Name,
			Path: release.Path,
			Type: "release-directory",
		})
	}

	artifacts, err := scanArtifacts(directory)
	if err != nil {
		return fmt.Errorf("scan %s: %w", directory, err)
	}
	if latestOnly {
		artifacts = latestArtifacts(artifacts)
	}
	results = append(results, artifacts...)

	jsonPath := filepath.Join(directory, "results.json")
	payload, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scan results: %w", err)
	}
	if err := os.WriteFile(jsonPath, payload, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}
	fmt.Fprintf(stdout, "\nSaved JSON results to: %s\n", jsonPath)
	return nil
}
