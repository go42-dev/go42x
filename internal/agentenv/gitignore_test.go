package agentenv

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/config"
)

func TestGitignoreUpdatePreservesCustomRules(t *testing.T) {
	root := t.TempDir()
	block, err := projectGitIgnore(root, config.DefaultBuildDir)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		filepath.Join(root, ".gitignore"):        block,
		filepath.Join(root, ".go42x/.gitignore"): assets.AgentEnvDirGitIgnore(),
	}
	for path := range paths {
		writeFile(t, path, "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n")
	}

	for range 2 {
		if err := updateGitIgnore(root, config.DefaultBuildDir); err != nil {
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

	if err := updateGitIgnore(root, config.DefaultBuildDir); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, filepath.Join(root, ".go42x/.gitignore"))); got != assets.AgentEnvDirGitIgnore() {
		t.Fatalf(".go42x/.gitignore = %q", got)
	}
}

func TestGitignoreUpdateWithoutBuildDirKeepsProjectBlock(t *testing.T) {
	root := t.TempDir()
	project := "# go42x -- START\n/out/\n# go42x -- END\n"
	writeFile(t, filepath.Join(root, ".gitignore"), project)

	if err := updateGitIgnore(root, ""); err != nil {
		t.Fatal(err)
	}
	if got := string(readFile(t, filepath.Join(root, ".gitignore"))); got != project {
		t.Fatalf(".gitignore = %q, want it unchanged", got)
	}
	if got := string(readFile(t, filepath.Join(root, ".go42x/.gitignore"))); got != assets.AgentEnvDirGitIgnore() {
		t.Fatalf(".go42x/.gitignore = %q", got)
	}
}

func TestGitignoreUpdateRejectsMalformedMarkers(t *testing.T) {
	for _, path := range []string{".gitignore", ".go42x/.gitignore"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, path), "# go42x -- START\ncustom/\n")
			before := snapshotTree(t, root)

			if err := updateGitIgnore(root, config.DefaultBuildDir); err == nil {
				t.Fatal("malformed gitignore accepted")
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("invalid gitignore caused output or backup writes")
			}
		})
	}
}

func TestProjectGitIgnoreBuildDir(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	ignored := func(path string) bool {
		t.Helper()
		err := exec.Command("git", "-C", root, "check-ignore", "-q", "--no-index", "--", path).Run()
		var exit *exec.ExitError
		if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
			t.Fatalf("git check-ignore %s: %v", path, err)
		}
		return err == nil
	}

	for _, tt := range []struct {
		name, buildDir, path, line string
	}{
		{"default", config.DefaultBuildDir, ".build", "/.build/"},
		{"nested", "out/build", "out/build", "/out/build/"},
		{"absolute inside", filepath.Join(root, "dist"), "dist", "/dist/"},
		{"wildcards", "a[1]*?", "a[1]*?", `/a\[1]\*\?/`},
		{"project root", ".", "", ""},
		{"parent", "../shared", "", ""},
		{"outside", t.TempDir(), "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			block, err := projectGitIgnore(root, tt.buildDir)
			if err != nil {
				t.Fatal(err)
			}
			want := "# go42x -- START\n.mcp.json\n"
			if len(tt.line) > 0 {
				want = "# go42x -- START\n" + tt.line + "\n.mcp.json\n"
			}
			if !strings.HasPrefix(block, want) || strings.Contains(block, "{{") {
				t.Fatalf("block = %q, want prefix %q", block, want)
			}
			if len(tt.path) == 0 {
				return
			}

			writeFile(t, filepath.Join(root, ".gitignore"), block)
			if !ignored(tt.path + "/probe") {
				t.Errorf("%s is not ignored", tt.path)
			}
			if ignored("sub/" + tt.path + "/probe") {
				t.Errorf("sub/%s is ignored; the pattern must be anchored to the root", tt.path)
			}
		})
	}
}
