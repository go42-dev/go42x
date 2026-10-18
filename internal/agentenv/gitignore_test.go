package agentenv

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go42-dev/go42x/assets"
)

func TestGitignoreUpdatePreservesCustomRules(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".gitignore")
	writeFile(t, path, "before\n# go42x -- START\nobsolete\n# go42x -- END\nafter\n")

	for range 2 {
		if err := updateGitIgnore(root); err != nil {
			t.Fatal(err)
		}
		if got := string(readFile(t, path)); got != "before\n"+assets.AgentEnvGitIgnore()+"after\n" {
			t.Fatalf("gitignore = %q", got)
		}
	}
}

func TestGitignoreUpdateRejectsMalformedMarkers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "# go42x -- START\ncustom/\n")
	before := snapshotTree(t, root)

	if err := updateGitIgnore(root); err == nil {
		t.Fatal("malformed gitignore accepted")
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("invalid gitignore caused output or backup writes")
	}
}
