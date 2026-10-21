package releases

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestListDirectoriesMatchesReleaseNames(t *testing.T) {
	root := t.TempDir()

	for _, p := range []string{
		"4043/4043.206",
		"4043/4043.85",
		"4043/4043.85/Corporate_Host_42.5.85-4043",
		"4043/4043.85/ServerSide/ACH_WebService_Auto_17.3.85-4043",
		"4043/4043.85/ClientSide/Paya_4.5.85-4043",
		"other/ignored-dir",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}

	results, err := LatestInDirectory(root)
	if err != nil {
		t.Fatalf("LatestInDirectory() error = %v", err)
	}
	got := make([]string, 0, len(results))
	for _, result := range results {
		got = append(got, result.Name)
	}
	sort.Strings(got)

	want := []string{
		"ACH_WebService_Auto_17.3.85-4043",
	}
	if len(got) != len(want) {
		t.Fatalf("listDirectories() returned %d directories, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listDirectories()[%d] = %q, want %q; got %#v", i, got[i], want[i], got)
		}
	}
}

func TestLatestVersionNames(t *testing.T) {
	items := []string{
		"Corporate_Host_42.5.90-4043",
		"Corporate_Host_42.5.100-4043",
		"Corporate_Host_42.5.99-4043",
		"ACH_WebService_Auto_17.3.90-4043",
		"ACH_WebService_Auto_17.3.100-4043",
		"ServerSide",
	}

	results := make([]Result, 0, len(items))
	for _, name := range items {
		results = append(results, Result{Name: name})
	}
	selected := Latest(results)
	got := make([]string, 0, len(selected))
	for _, result := range selected {
		got = append(got, result.Name)
	}
	sort.Strings(got)

	want := []string{
		"ACH_WebService_Auto_17.3.100-4043",
		"Corporate_Host_42.5.100-4043",
	}
	if len(got) != len(want) {
		t.Fatalf("latestVersionNames() returned %d items, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("latestVersionNames()[%d] = %q, want %q; got %#v", i, got[i], want[i], got)
		}
	}
}
