package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afshinnkarimi/projscan/internal/scanner"
)

func TestHelpFlags(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{arg}, &stdout, &stderr); code != 0 {
				t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
			}
			output := stdout.String() + stderr.String()
			for _, want := range []string{"Usage:", "--directory", "--dir", "--latest", "-h, --help"} {
				if !strings.Contains(output, want) {
					t.Errorf("help output does not contain %q: %s", want, output)
				}
			}
		})
	}
}

func TestDirectoryFlagAndLatestFlag(t *testing.T) {
	root := t.TempDir()
	for _, chart := range []struct {
		directory string
		version   string
	}{
		{directory: "old", version: "1.9.0"},
		{directory: "new", version: "1.10.0"},
	} {
		chartDirectory := filepath.Join(root, chart.directory)
		if err := os.MkdirAll(chartDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		data := []byte("apiVersion: v2\nname: example\nversion: " + chart.version + "\n")
		if err := os.WriteFile(filepath.Join(chartDirectory, "Chart.yaml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--latest", "--directory", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var results []scanner.Result
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Inventory.Metadata["version"] != "1.10.0" {
		t.Fatalf("results = %#v, want only latest chart 1.10.0", results)
	}
}

func TestDirectoryDefaultsToCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "results.json")); err != nil {
		t.Fatalf("results.json was not written to current directory: %v", err)
	}
}

func TestDirectoryEnvironmentVariable(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PROJSCAN_DIRECTORY", directory)

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "results.json")); err != nil {
		t.Fatalf("environment directory was not scanned: %v", err)
	}
}
