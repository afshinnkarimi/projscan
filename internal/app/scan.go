package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/afshinnkarimi/projscan/internal/releases"
	"github.com/afshinnkarimi/projscan/internal/scanner"
)

func Scan(directory string, latestOnly bool, output io.Writer) error {
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("access scan directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan target is not a directory: %s", directory)
	}

	releaseResults, err := releases.LatestInDirectory(directory)
	if err != nil {
		return fmt.Errorf("find releases in %s: %w", directory, err)
	}
	artifactResults, err := scanner.ScanArtifacts(directory)
	if err != nil {
		return fmt.Errorf("scan artifacts in %s: %w", directory, err)
	}
	if latestOnly {
		artifactResults = scanner.LatestArtifacts(artifactResults)
	}

	results := make([]scanner.Result, 0, len(releaseResults)+len(artifactResults))
	for _, release := range releaseResults {
		results = append(results, scanner.Result{
			Name: release.Name,
			Path: release.Path,
			Type: "release-directory",
		})
		fmt.Fprintln(output, release.Name)
	}
	results = append(results, artifactResults...)

	resultsPath := filepath.Join(directory, "results.json")
	payload, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scan results: %w", err)
	}
	if err := os.WriteFile(resultsPath, payload, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", resultsPath, err)
	}
	fmt.Fprintf(output, "\nSaved JSON results to: %s\n", resultsPath)
	return nil
}
