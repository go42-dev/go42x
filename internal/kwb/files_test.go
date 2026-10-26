package kwb

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/config"
)

func TestEnsureSourceIgnore(t *testing.T) {
	block := assets.KWBIgnore()[strings.Index(assets.KWBIgnore(), "# go42x -- START"):]
	for _, tt := range []struct {
		name     string
		exists   bool
		previous string
		want     string
	}{
		{
			name: "missing file",
			want: assets.KWBIgnore(),
		},
		{
			name:   "empty file",
			exists: true,
			want:   assets.KWBIgnore(),
		},
		{
			name:     "custom rules",
			exists:   true,
			previous: "custom/**\n!custom/keep.md\n",
			want:     "custom/**\n!custom/keep.md\n\n" + block,
		},
		{
			name:     "old managed block",
			exists:   true,
			previous: "# go42x -- START\nobsolete\n# go42x -- END\n",
			want:     block,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			manager := newIndexManager(slog.New(slog.DiscardHandler), NewSettings())

			if tt.exists {
				writeFixture(t, root, sourceIgnorePath, tt.previous)
			}

			backup, err := manager.ensureSourceIgnore(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, sourceIgnorePath))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Fatalf("ignore file = %q, want %q", data, tt.want)
			}
			if tt.exists {
				original, err := os.ReadFile(filepath.Join(backup, "files", sourceIgnorePath))
				if err != nil || string(original) != tt.previous {
					t.Fatalf("backup = %q, %v; want %q", original, err, tt.previous)
				}
			} else if len(backup) > 0 {
				t.Fatalf("new file created an unnecessary backup: %s", backup)
			}

			if repeated, err := manager.ensureSourceIgnore(t.Context(), root); err != nil || len(repeated) > 0 {
				t.Fatalf("unchanged rules created another backup: %q, %v", repeated, err)
			}
		})
	}
}

func TestEnsureSourceIgnoreCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	manager := newIndexManager(slog.New(slog.DiscardHandler), NewSettings())

	if _, err := manager.ensureSourceIgnore(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled creation = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, config.Directory)); !os.IsNotExist(err) {
		t.Fatalf("canceled creation changed project: %v", err)
	}
}

func TestSourceExclusionsUsesBundledDefaultsAndCustomExceptions(t *testing.T) {
	root := t.TempDir()
	manager := newIndexManager(slog.New(slog.DiscardHandler), NewSettings())
	if _, err := manager.ensureSourceIgnore(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	matcher, err := manager.sourceExclusions(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	path := []string{".github", "workflows", "agent.lock.yml"}

	if !matcher.Match(path, false) {
		t.Fatal("bundled defaults did not exclude a compiled workflow")
	}
	if matcher.Match([]string{".github", "workflows", "agent.md"}, false) ||
		matcher.Match([]string{".github", "aw", "actions-lock.json"}, false) {
		t.Fatal("bundled defaults excluded authored workflows or action pins")
	}

	writeFixture(t, root, sourceIgnorePath, assets.KWBIgnore()+"!.github/workflows/agent.lock.yml\n")
	matcher, err = manager.sourceExclusions(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if matcher.Match(path, false) {
		t.Fatal("custom exception did not override the managed block")
	}
}

func TestSourceExclusionsCanIncludeAgentFiles(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		".gitignore":              ".go42x/private.md\n",
		".go42x/go42x.local.yaml": "project: local",
		".go42x/backups/keep.md":  "Retained reference",
		".go42x/backups/skip.md":  "Backup",
		".go42x/private.md":       "Private reference",
		sourceIgnorePath: assets.KWBIgnore() + "!.go42x/backups/keep.md\n" +
			"!.go42x/go42x.local.yaml\n!.go42x/private.md\n",
	}
	for path, content := range fixtures {
		writeFixture(t, root, path, content)
	}
	manager := newIndexManager(slog.New(slog.DiscardHandler), NewSettings())
	var paths []string

	err := manager.walkFiles(t.Context(), root, func(path string, _ os.FileInfo, _ []byte) error {
		paths = append(paths, path)
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".gitignore",
		".go42x/backups/keep.md",
		".go42x/go42x.local.yaml",
		sourceIgnorePath,
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("files after custom exceptions = %v, want %v", paths, want)
	}
}
