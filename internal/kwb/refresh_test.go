package kwb

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editManifest rewrites the manifest of the current generation through edit.
func editManifest(t *testing.T, indexPath string, edit func(map[string]any)) {
	t.Helper()
	current, err := os.ReadFile(filepath.Join(indexPath, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(indexPath, strings.TrimSpace(string(current)), "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	edit(document)
	if data, err = json.Marshal(document); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshIndexKeepsRecordedSelection(t *testing.T) {
	t.Parallel()
	root, index := t.TempDir(), filepath.Join(t.TempDir(), "index")
	builder := testService(t, root, index, func(settings *Settings) {
		settings.ExtraExtensions = []string{"custom"}
		settings.ExcludeDirs = []string{"out"}
	})
	writeFixture(t, root, "notes.custom", "first custom note\n")
	buildFixture(t, builder, root)
	writeFixture(t, root, "later.custom", "second custom note\n")
	writeFixture(t, root, "out/report.md", "# Report\nexcluded output\n")

	// A server uses default settings; the refresh must keep the recorded ones.
	server := testService(t, root, index, nil)
	report, err := server.RefreshIndex(t.Context())
	if err != nil || report.Indexed != 1 || report.Rebuilt {
		t.Fatalf("refresh: %+v %v", report, err)
	}
	for query, want := range map[string]uint64{"first": 1, "second": 1, "excluded": 0} {
		result, err := server.Search(t.Context(), SearchOptions{Query: query})
		if err != nil || result.Total != want {
			t.Errorf("search %q: %+v %v", query, result, err)
		}
	}
	freshness, err := server.CheckFreshness(t.Context())
	if err != nil || freshness.Status != FreshnessFresh {
		t.Fatalf("freshness after refresh: %+v %v", freshness, err)
	}
}

func TestRefreshIndexLeavesExclusionsUnchanged(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := testService(t, root, filepath.Join(t.TempDir(), "index"), nil)
	writeFixture(t, root, "guide.md", "# Guide\n")
	buildFixture(t, service, root)
	outdated := "# go42x -- START\nold.md\n# go42x -- END\ncustom.md\n"
	writeFixture(t, root, ".go42x/kwb.ignore", outdated)
	writeFixture(t, root, "old.md", "# Old\nretired\n")
	writeFixture(t, root, "custom.md", "# Custom\nignored\n")

	if _, err := service.RefreshIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".go42x/kwb.ignore"))
	if err != nil || string(data) != outdated {
		t.Fatalf("refresh rewrote exclusions: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".go42x/backups")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("refresh wrote backups: %v", err)
	}
	// The live rules still select sources.
	for _, query := range []string{"retired", "ignored"} {
		result, err := service.Search(t.Context(), SearchOptions{Query: query})
		if err != nil || result.Total != 0 {
			t.Errorf("excluded source %q indexed: %+v %v", query, result, err)
		}
	}
}

func TestRefreshIndexRequiresAnIndexWithSettings(t *testing.T) {
	t.Parallel()
	root, index := t.TempDir(), filepath.Join(t.TempDir(), "index")
	service := testService(t, root, index, nil)
	writeFixture(t, root, "guide.md", "# Guide\n")
	if _, err := service.RefreshIndex(t.Context()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing index: %v", err)
	}
	for _, path := range []string{index, filepath.Join(root, ".go42x")} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("refresh created %s: %v", path, err)
		}
	}

	buildFixture(t, service, root)
	editManifest(t, index, func(document map[string]any) {
		delete(document, "selection")
	})
	if _, err := service.RefreshIndex(t.Context()); !errors.Is(err, errMissingSelection) {
		t.Fatalf("index without settings: %v", err)
	}
}

func TestRefreshIndexRebuildsOlderFormat(t *testing.T) {
	t.Parallel()
	root, index := t.TempDir(), filepath.Join(t.TempDir(), "index")
	service := testService(t, root, index, nil)
	writeFixture(t, root, "guide.md", "# Guide\nformat marker\n")
	buildFixture(t, service, root)
	editManifest(t, index, func(document map[string]any) {
		document["version"] = schemaVersion - 1
	})
	if _, err := service.GetStats(t.Context()); !errors.Is(err, ErrIndexFormat) {
		t.Fatalf("older format accepted: %v", err)
	}

	report, err := service.RefreshIndex(t.Context())
	if err != nil || !report.Rebuilt {
		t.Fatalf("refresh: %+v %v", report, err)
	}
	result, err := service.Search(t.Context(), SearchOptions{Query: "format marker"})
	if err != nil || result.Total != 1 {
		t.Fatalf("search after rebuild: %+v %v", result, err)
	}
}

func TestSearchMarksStaleResults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := testService(t, root, filepath.Join(t.TempDir(), "index"), nil)
	for _, name := range []string{"same", "edited", "resized", "deleted"} {
		writeFixture(t, root, name+".md", "# "+name+"\nshared marker\n")
	}
	buildFixture(t, service, root)
	writeFixture(t, root, "edited.md", "# edited\nshared markex\n")
	writeFixture(t, root, "resized.md", "# resized\nshared marker, longer\n")
	if err := os.Remove(filepath.Join(root, "deleted.md")); err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(t.Context(), SearchOptions{Query: "shared marker"})
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]bool{}
	for _, hit := range result.Results {
		stale[hit.Path] = hit.Stale
	}
	want := map[string]bool{"same.md": false, "edited.md": true, "resized.md": true, "deleted.md": true}
	if len(stale) != len(want) {
		t.Fatalf("results = %+v", result.Results)
	}
	for path, wantStale := range want {
		if stale[path] != wantStale {
			t.Errorf("%s stale = %v, want %v", path, stale[path], wantStale)
		}
	}
}
