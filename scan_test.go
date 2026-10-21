package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunHelpFlags(t *testing.T) {
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
			if strings.Contains(output, "--config") {
				t.Errorf("help output still advertises config-file support: %s", output)
			}
		})
	}
}

func TestRunUsesCurrentDirectoryByDefault(t *testing.T) {
	root := t.TempDir()
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()

	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "results.json")); err != nil {
		t.Fatalf("results.json was not written in the current directory: %v", err)
	}
	if !strings.Contains(stdout.String(), "Saved JSON results to: "+filepath.Join(root, "results.json")) {
		t.Errorf("output does not mention results path: %s", stdout.String())
	}
}

func TestRunDirectoryFlag(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--directory", target}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(target, "results.json")); err != nil {
		t.Fatalf("results.json was not written to the selected directory: %v", err)
	}
}

func TestRunDirectoryAlias(t *testing.T) {
	target := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--dir", target}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(target, "results.json")); err != nil {
		t.Fatalf("results.json was not written to the selected directory: %v", err)
	}
}

func TestRunLatestFlagFiltersResults(t *testing.T) {
	root := t.TempDir()
	for _, chart := range []struct {
		directory string
		version   string
	}{
		{directory: "old", version: "1.9.0"},
		{directory: "new", version: "1.10.0"},
	} {
		chartDir := filepath.Join(root, chart.directory)
		if err := os.MkdirAll(chartDir, 0o755); err != nil {
			t.Fatal(err)
		}
		metadata := "apiVersion: v2\nname: example\nversion: " + chart.version + "\n"
		if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTestTar(t, filepath.Join(root, "example-1.10.0.tgz"), map[string]string{
		"example/Chart.yaml": "apiVersion: v2\nname: example\nversion: 1.10.0\n",
	}, true)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--latest", "--directory", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var results []scanResult
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("--latest results = %#v, want only chart version 1.10.0", results)
	}
	charts := helmChartVersions(results[0].Inventory)
	if len(charts) != 1 || charts[0].name != "example" || charts[0].version != "1.10.0" {
		t.Fatalf("--latest chart inventory = %#v, want example version 1.10.0", charts)
	}
}

func TestRunReadsViperEnvironment(t *testing.T) {
	envTarget := t.TempDir()
	t.Setenv("PROJSCAN_DIRECTORY", envTarget)
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("run() exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(envTarget, "results.json")); err != nil {
		t.Fatalf("environment setting was not used: %v", err)
	}
}

func TestScanArtifactsByContents(t *testing.T) {
	root := t.TempDir()

	chartDir := filepath.Join(root, "charts", "sample")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: sample\nversion: 1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestTar(t, filepath.Join(root, "chart-package.tar"), map[string]string{
		"sample/Chart.yaml":                "apiVersion: v2\nname: sample-tar\nversion: 1.0.0\n",
		"sample/templates/deployment.yaml": "kind: Deployment\n",
	}, false)
	writeTestTar(t, filepath.Join(root, "chart-package.tar.gz"), map[string]string{
		"sample/Chart.yaml": "apiVersion: v2\nname: sample-gzip\nversion: 1.0.0\n",
	}, true)

	dockerManifest, err := json.Marshal([]dockerManifestEntry{{
		Config:   "blobs/config.json",
		RepoTags: []string{"example/app:1.0"},
		Layers:   []string{"blobs/layer.tar"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeTestTar(t, filepath.Join(root, "image.tar"), map[string]string{
		"image/manifest.json":     string(dockerManifest),
		"image/blobs/config.json": "{}",
		"image/blobs/layer.tar":   "layer",
	}, false)

	writeTestTar(t, filepath.Join(root, "java-app.tar.gz"), map[string]string{
		"app/pom.xml": `<project><dependencies><dependency><groupId>org.example</groupId><artifactId>library</artifactId><version>2.4</version></dependency></dependencies></project>`,
		"app/app.jar": testJar(t, "Implementation-Title: example-app\nImplementation-Version: 3.4.1\nMain-Class: org.example.Main\n"),
	}, true)

	results, err := scanArtifacts(root)
	if err != nil {
		t.Fatalf("scanArtifacts() error = %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("scanArtifacts() returned %d results, want 5: %#v", len(results), results)
	}

	byName := make(map[string]scanResult)
	for _, result := range results {
		byName[result.Name] = result
	}

	docker := byName["image.tar"]
	if docker.Type != "docker-image" {
		t.Fatalf("Docker archive type = %q, want docker-image", docker.Type)
	}
	if docker.Inventory.Image == nil ||
		!reflect.DeepEqual(docker.Inventory.Image.Tags, []string{"example/app:1.0"}) ||
		!reflect.DeepEqual(docker.Inventory.Image.Layers, []string{"blobs/layer.tar"}) {
		t.Fatalf("Docker inventory = %#v", docker.Inventory)
	}

	chart := byName["sample"]
	if chart.Type != "helm-chart" ||
		chart.Inventory.Metadata["name"] != "sample" ||
		chart.Inventory.Metadata["version"] != "1.2.3" {
		t.Fatalf("Helm chart result = %#v", chart)
	}
	if !reflect.DeepEqual(chart.Inventory.Files, []string{"Chart.yaml", "templates/deployment.yaml"}) {
		t.Fatalf("Helm chart files = %#v", chart.Inventory.Files)
	}
	if tarChart := byName["chart-package.tar"]; tarChart.Type != "helm-chart" {
		t.Fatalf("Helm tar result = %#v", tarChart)
	}
	if gzipChart := byName["chart-package.tar.gz"]; gzipChart.Type != "helm-chart" {
		t.Fatalf("Helm tar.gz result = %#v", gzipChart)
	}

	java := byName["java-app.tar.gz"]
	if java.Type != "java-application" ||
		java.Inventory.Metadata["containerized"] != "false" ||
		java.Inventory.Metadata["application"] != "example-app" ||
		java.Inventory.Metadata["version"] != "3.4.1" ||
		!reflect.DeepEqual(java.Inventory.Dependencies, []string{"org.example:library:2.4"}) {
		t.Fatalf("Java application result = %#v", java)
	}
}

func TestLatestArtifactsKeepsOnlyLatestSemanticVersions(t *testing.T) {
	results := []scanResult{
		{
			Name: "app-old.tar", Type: "docker-image",
			Inventory: artifactInventory{Image: &dockerImageDetails{Tags: []string{"registry.example/app:1.2.0", "registry.example/app:latest"}}},
		},
		{
			Name: "app-new.tar", Type: "docker-image",
			Inventory: artifactInventory{Image: &dockerImageDetails{Tags: []string{"registry.example/app:v1.10.0", "registry.example/app:stable"}}},
		},
		{
			Name: "chart-old.tgz", Path: "chart-old.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{"name": "example-chart", "version": "2.1.0"}},
		},
		{
			Name: "chart-new.tgz", Path: "m-original.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{"name": "example-chart", "version": "2.10.0"}},
		},
		{
			Name: "chart-new-copy.tgz", Path: "z-copy.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{"name": "example-chart", "version": "2.10.0"}},
		},
		{
			Name: "chart-new-preferred.tgz", Path: "a-copy.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{"name": "example-chart", "version": "2.10.0"}},
		},
		{
			Name: "java-old.tar.gz", Type: "java-application", latestKey: "java:example-app", latestTag: "3.9.0",
		},
		{
			Name: "java-new.tar.gz", Type: "java-application", latestKey: "java:example-app", latestTag: "3.10.0",
		},
		{
			Name: "unversioned.tar", Type: "unknown",
		},
	}

	got := latestArtifacts(results)
	gotNames := make([]string, 0, len(got))
	for _, result := range got {
		gotNames = append(gotNames, result.Name)
	}
	wantNames := []string{"app-new.tar", "chart-new-preferred.tgz", "java-new.tar.gz", "unversioned.tar"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("latestArtifacts() names = %#v, want %#v", gotNames, wantNames)
	}
	if !reflect.DeepEqual(got[0].Inventory.Image.Tags, []string{"registry.example/app:v1.10.0"}) {
		t.Fatalf("latest Docker tags = %#v", got[0].Inventory.Image.Tags)
	}
}

func TestLatestHelmChartsSupportsPrefixedMetadataAndTwoPartVersions(t *testing.T) {
	results := []scanResult{
		{
			Name: "mychart-directory", Path: "a/mychart", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"name": "mychart", "version": "3.43",
			}},
		},
		{
			Name: "mychart-3.52.tgz", Path: "b/mychart-3.52.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"mychart/name": "mychart", "mychart/version": "3.52",
			}},
		},
		{
			Name: "mychart-4.48.0.tgz", Path: "c/mychart-4.48.0.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"mychart/name": "mychart", "mychart/version": "4.48.0",
			}},
		},
		{
			Name: "otherchart-directory", Path: "d/otherchart", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"otherchart/name": "otherchart", "otherchart/version": "1.0.0",
			}},
		},
	}

	got := latestArtifacts(results)
	gotNames := make([]string, 0, len(got))
	for _, result := range got {
		gotNames = append(gotNames, result.Name)
	}
	want := []string{"mychart-4.48.0.tgz", "otherchart-directory"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("latest Helm charts = %#v, want %#v", gotNames, want)
	}
}

func TestLatestHelmIgnoresSubchartVersionsWhenSelectingParentChart(t *testing.T) {
	results := []scanResult{
		{
			Name: "cilium", Path: "a/cilium", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"name":    "cilium",
				"version": "1.20.3",
			}},
		},
		{
			Name: "cilium-1.6.9.tgz", Path: "b/cilium-1.6.9.tgz", Type: "helm-chart",
			Inventory: artifactInventory{Metadata: map[string]string{
				"cilium/name":                    "cilium",
				"cilium/version":                 "1.6.9",
				"cilium/charts/agent/name":       "agent",
				"cilium/charts/agent/version":    "1.6.9",
				"cilium/charts/operator/name":    "operator",
				"cilium/charts/operator/version": "1.6.9",
			}},
		},
	}

	got := latestArtifacts(results)
	if len(got) != 1 || got[0].Name != "cilium" {
		t.Fatalf("latest Cilium chart = %#v, want only directory chart version 1.20.3", got)
	}
}

func TestLatestHelmChartsAcrossDirectoryAndArchives(t *testing.T) {
	root := t.TempDir()
	chartDirectory := filepath.Join(root, "mychart")
	if err := os.MkdirAll(chartDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDirectory, "Chart.yaml"), []byte("apiVersion: v2\nname: mychart\nversion: 3.43\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestTar(t, filepath.Join(root, "mychart-3.52.tgz"), map[string]string{
		"mychart/Chart.yaml": "apiVersion: v2\nname: mychart\nversion: 3.52\n",
	}, true)
	writeTestTar(t, filepath.Join(root, "mychart-4.48.0.tgz"), map[string]string{
		"mychart/Chart.yaml": "apiVersion: v2\nname: mychart\nversion: 4.48.0\n",
	}, true)

	results, err := scanArtifacts(root)
	if err != nil {
		t.Fatalf("scanArtifacts() error = %v", err)
	}
	got := latestArtifacts(results)
	if len(got) != 1 || got[0].Name != "mychart-4.48.0.tgz" {
		t.Fatalf("latest chart across directories and archives = %#v, want mychart-4.48.0.tgz", got)
	}
}

func TestJavaLatestUsesArchiveFilename(t *testing.T) {
	root := t.TempDir()
	for _, archive := range []struct {
		name            string
		manifestVersion string
	}{
		{name: "java-1.1.0.tar.gz", manifestVersion: "99.0.0"},
		{name: "java-1.2.0.tar.gz", manifestVersion: "1.0.0"},
	} {
		writeTestTar(t, filepath.Join(root, archive.name), map[string]string{
			"app.jar": testJar(t, "Implementation-Title: java-app\nImplementation-Version: "+archive.manifestVersion+"\nMain-Class: org.example.Main\n"),
		}, true)
	}

	var results []scanResult
	for _, name := range []string{"java-1.1.0.tar.gz", "java-1.2.0.tar.gz"} {
		results = append(results, scanTarArchive(filepath.Join(root, name)))
	}
	got := latestArtifacts(results)
	if len(got) != 1 || got[0].Name != "java-1.2.0.tar.gz" {
		t.Fatalf("latest Java archive = %#v, want java-1.2.0.tar.gz", got)
	}
}

func TestSemanticVersionComparisonIncludesPrereleases(t *testing.T) {
	older, ok := parseSemanticVersion("1.2.3-rc.9")
	if !ok {
		t.Fatal("failed to parse prerelease version")
	}
	newer, ok := parseSemanticVersion("v1.2.3")
	if !ok {
		t.Fatal("failed to parse release version")
	}
	if compareSemanticVersions(newer, older) <= 0 {
		t.Fatal("stable release should be newer than prerelease")
	}
}

func TestScanUnknownArchiveReportsError(t *testing.T) {
	root := t.TempDir()
	writeTestTar(t, filepath.Join(root, "other.tar"), map[string]string{
		"readme.txt": "not a supported artifact",
	}, false)

	results, err := scanArtifacts(root)
	if err != nil {
		t.Fatalf("scanArtifacts() error = %v", err)
	}
	if len(results) != 1 || results[0].Type != "unknown" || len(results[0].Errors) == 0 {
		t.Fatalf("unknown archive result = %#v", results)
	}
}

func writeTestTar(t *testing.T, filePath string, files map[string]string, compressed bool) {
	t.Helper()
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}

	var gzipWriter *gzip.Writer
	var writer *tar.Writer
	if compressed {
		gzipWriter = gzip.NewWriter(file)
		writer = tar.NewWriter(gzipWriter)
	} else {
		writer = tar.NewWriter(file)
	}
	for name, contents := range files {
		data := []byte(contents)
		if err := writer.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(data)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if gzipWriter != nil {
		if err := gzipWriter.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func testJar(t *testing.T, manifest string) string {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	file, err := writer.Create("META-INF/MANIFEST.MF")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.String()
}
