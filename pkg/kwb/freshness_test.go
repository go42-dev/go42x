package kwb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAuthoredCoverageAndProjectExclusions(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		".env.example": "EXAMPLE=value", ".go42x/go42x.yaml": "project: sample",
		".go42x/agents.tpl.md": "Agent instructions", ".go42x/chunks/team/rules.tpl.md": "Project rules",
		".go42x/go42x.local.yaml": "local only", ".go42x/go42x.schema.json": "{}",
		".go42x/backups/old.tpl.md": "backup", ".go42x/state.json": "{}",
		".go42x/chunks/generated.json": "{}", ".go42x/kwb/other/state.md": "index state",
		".gitignore": ".go42x/chunks/private.tpl.md\n", ".go42x/chunks/private.tpl.md": "private",
		".go42x/kwb.ignore":    "static/**\n!static/keep.min.js\n!static/handwritten.js\n!static/flagged.js\n",
		"static/bundle.min.js": "generated bundle", "static/keep.min.js": "kept by exception",
		"static/handwritten.js": "const handwritten = true", "static/flagged.js": "excluded by flag",
	} {
		writeFixture(t, root, path, content)
	}
	if err := os.Symlink("go42x.yaml", filepath.Join(root, ".go42x/link.tpl.md")); err != nil {
		t.Fatal(err)
	}
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), func(s *Settings) {
		s.ExcludeFiles = []string{"static/flagged.js"}
	})
	buildFixture(t, service, root)
	files, err := service.ListFiles(t.Context(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, file := range files.Files {
		paths = append(paths, file.Path)
	}
	want := []string{".env.example", ".gitignore", ".go42x/agents.tpl.md", ".go42x/chunks/team/rules.tpl.md",
		".go42x/go42x.yaml", ".go42x/kwb.ignore", "static/handwritten.js", "static/keep.min.js"}
	if !slices.Equal(paths, want) {
		t.Fatalf("coverage: %v, want %v", paths, want)
	}
	writeFixture(t, root, sourceIgnorePath, "static/*.js\n")
	before, err := service.CheckFreshness(t.Context())
	if err != nil || !before.Complete || before.Excluded.Count != 2 || before.Changed.Count != 1 {
		t.Fatalf("policy changes: %+v %v", before, err)
	}
	buildFixture(t, service, root)
	after, err := service.CheckFreshness(t.Context())
	if err != nil || after.Status != "fresh" {
		t.Fatalf("updated policy: %+v %v", after, err)
	}
}

func TestFreshnessClassifiesChangesAndRetainsBuildPolicy(t *testing.T) {
	for _, backend := range []string{"scorch", "upsidedown"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			index := filepath.Join(root, ".go42x/kwb/index")
			for _, path := range []string{"changed.md", "deleted.md", "ignored.md", "custom.xyz"} {
				writeFixture(t, root, path, "original content\n")
			}
			builder := testService(t, root, index, func(s *Settings) {
				s.ExtraExtensions = []string{".xyz"}
				s.IndexType = backend
			})
			buildFixture(t, builder, root)
			reader := testService(t, root, index, nil)
			fresh, err := reader.CheckFreshness(t.Context())
			if err != nil || !fresh.Complete || fresh.Status != "fresh" || fresh.CheckedFiles != 5 {
				t.Fatalf("fresh: %+v %v", fresh, err)
			}
			writeFixture(t, root, "new.md", "new file\n")
			writeFixture(t, root, "changed.md", "changed content\n")
			writeFixture(t, root, "custom.xyz", "custom extension changed\n")
			writeFixture(t, root, ".gitignore", "ignored.md\n")
			if err := os.Remove(filepath.Join(root, "deleted.md")); err != nil {
				t.Fatal(err)
			}
			stale, err := reader.CheckFreshness(t.Context())
			if err != nil || !stale.Complete || stale.Status != "stale" || stale.Generation != fresh.Generation ||
				stale.New.Count != 2 || stale.Changed.Count != 2 || stale.Deleted.Count != 1 || stale.Excluded.Count != 1 {
				t.Fatalf("stale: %+v %v", stale, err)
			}
			again, err := reader.CheckFreshness(t.Context())
			if err != nil || !reflect.DeepEqual(stale, again) {
				t.Fatalf("nondeterministic check: %+v %v", again, err)
			}
			stats, err := reader.GetStats(t.Context())
			if err != nil || stats.Generation != fresh.Generation {
				t.Fatalf("check changed generation: %+v %v", stats, err)
			}
			buildFixture(t, builder, root)
			updated, err := reader.CheckFreshness(t.Context())
			if err != nil || updated.Status != "fresh" || updated.Generation == fresh.Generation {
				t.Fatalf("refresh: %+v %v", updated, err)
			}
			buildFixture(t, builder, root)
			unchanged, err := reader.CheckFreshness(t.Context())
			if err != nil || unchanged.Generation != updated.Generation {
				t.Fatalf("no-op refresh: %+v %v", unchanged, err)
			}
		})
	}
}

func TestFreshnessMissingRootMismatchAndDeadline(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), nil)
	missing, err := service.CheckFreshness(t.Context())
	if !errors.Is(err, os.ErrNotExist) || missing.Status != "missing" || missing.Complete {
		t.Fatalf("missing: %+v %v", missing, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only check created files: %v %v", entries, err)
	}
	buildFixture(t, service, root)
	other := testService(t, t.TempDir(), service.settings.IndexPath, func(s *Settings) { s.RequireRootMatch = true })
	if _, err := other.CheckFreshness(t.Context()); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("root mismatch: %v", err)
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	incomplete, err := service.CheckFreshness(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || incomplete.Complete || len(incomplete.Diagnostics) == 0 {
		t.Fatalf("deadline: %+v %v", incomplete, err)
	}
}

func TestFreshnessBoundedSamplesPreserveCounts(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), nil)
	buildFixture(t, service, root)
	for i := range 150 {
		writeFixture(t, root, fmt.Sprintf("%03d-%s.md", i, strings.Repeat("x", 120)), "new\n")
	}
	result, err := service.CheckFreshness(t.Context())
	if err != nil || !result.Complete || !result.Truncated || result.New.Count != 150 || len(result.New.Paths) > 100 {
		t.Fatalf("bounded check: %+v %v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > MaxResponseBytes {
		t.Fatalf("encoded response: %d %v", len(data), err)
	}
}

func TestFreshnessUpgradesLegacySelectionMetadata(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, filepath.Join(root, ".go42x/kwb/index"), nil)
	writeFixture(t, root, "source.md", "unchanged source\n")
	buildFixture(t, service, root)
	pointer, err := os.ReadFile(filepath.Join(service.settings.IndexPath, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(service.settings.IndexPath, strings.TrimSpace(string(pointer)))
	meta, err := readManifest(directory)
	if err != nil {
		t.Fatal(err)
	}
	meta.Selection = nil
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := service.CheckFreshness(t.Context())
	if err != nil || legacy.Complete || legacy.Status != "incomplete" || len(legacy.Diagnostics) == 0 ||
		legacy.Diagnostics[0].Code != "selection_unknown" {
		t.Fatalf("legacy selection: %+v %v", legacy, err)
	}
	buildFixture(t, service, root)
	updated, err := service.CheckFreshness(t.Context())
	if err != nil || updated.Status != "fresh" || updated.Generation == legacy.Generation {
		t.Fatalf("selection upgrade: %+v %v", updated, err)
	}
	buildFixture(t, service, root)
	unchanged, err := service.CheckFreshness(t.Context())
	if err != nil || unchanged.Generation != updated.Generation {
		t.Fatalf("selection upgrade repeated: %+v %v", unchanged, err)
	}
}
