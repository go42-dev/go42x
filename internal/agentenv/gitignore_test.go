package agentenv

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/config"
)

// gitIgnoreContext returns the template context fields that the managed .gitignore
// blocks read.
func gitIgnoreContext(tmpDir, journalPath string) map[string]any {
	return map[string]any{
		"context": config.Context{TmpDir: tmpDir}.WithDefaults(),
		"journal": config.Journal{Path: journalPath}.WithDefaults(),
	}
}

// renderAsset renders a bundled template with data.
func renderAsset(t *testing.T, path string, data map[string]any) string {
	t.Helper()
	block, err := assets.Render(path, data)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func TestGitignoreUpdatePreservesCustomRules(t *testing.T) {
	root := t.TempDir()
	data := gitIgnoreContext("", "")
	paths := map[string]string{
		filepath.Join(root, ".gitignore"):        renderAsset(t, assets.ProjectGitIgnore, data),
		filepath.Join(root, ".go42x/.gitignore"): renderAsset(t, assets.DirGitIgnore, data),
	}
	for path := range paths {
		writeFile(t, path, "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n")
	}

	for range 2 {
		if err := updateGitIgnore(root, data); err != nil {
			t.Fatal(err)
		}
		for path, block := range paths {
			if got := string(readFile(t, path)); got != "before\n"+block+"after\n" {
				t.Fatalf("%s = %q", path, got)
			}
		}
	}
}

func TestGitignoreUpdateCreatesDirectoryRules(t *testing.T) {
	root := t.TempDir()
	data := gitIgnoreContext("", "")

	if err := updateGitIgnore(root, data); err != nil {
		t.Fatal(err)
	}
	got := string(readFile(t, filepath.Join(root, ".go42x/.gitignore")))
	if got != renderAsset(t, assets.DirGitIgnore, data) || !strings.Contains(got, "\n/journal/\n/tmp/\n") {
		t.Fatalf(".go42x/.gitignore = %q", got)
	}
}

func TestDirGitIgnorePaths(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	for _, tt := range []struct {
		name, path, line string
	}{
		{"default", config.DefaultJournalPath, "/journal/"},
		{"nested", "state/journal", "/state/journal/"},
		{"wildcards", "notes[1]*", `/notes\[1]\*/`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// The block ignores a temporary directory and a journal at the same path alike.
			for _, data := range []map[string]any{
				gitIgnoreContext("", tt.path),
				gitIgnoreContext(tt.path, ""),
			} {
				block := renderAsset(t, assets.DirGitIgnore, data)
				if !strings.Contains(block, "\n"+tt.line+"\n") || strings.Contains(block, "{{") {
					t.Fatalf("block = %q, want line %q", block, tt.line)
				}

				writeFile(t, filepath.Join(root, ".go42x/.gitignore"), block)
				err := exec.Command("git", "-C", root, "check-ignore", "-q", "--no-index", "--",
					".go42x/"+tt.path+"/probe").Run()
				if err != nil {
					t.Errorf(".go42x/%s is not ignored: %v", tt.path, err)
				}
			}
		})
	}
}

func TestGitignoreUpdateRejectsMalformedMarkers(t *testing.T) {
	for _, path := range []string{".gitignore", ".go42x/.gitignore"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, path), "# go42x -- START\ncustom/\n")
			before := snapshotTree(t, root)

			if err := updateGitIgnore(root, gitIgnoreContext("", "")); err == nil {
				t.Fatal("malformed gitignore accepted")
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("invalid gitignore caused output or backup writes")
			}
		})
	}
}

func TestGitignoreUpdateRequiresTemplateContext(t *testing.T) {
	root := t.TempDir()
	if err := updateGitIgnore(root, nil); err == nil {
		t.Fatal("gitignore blocks rendered without the template context")
	}
}
