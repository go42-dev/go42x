package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/assets"
	"github.com/go42-dev/go42x/internal/kwb"
)

func TestKnowledgeBaseBuildRefreshesExclusions(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, ".go42x/kwb.ignore", "caller exclusions\n")
	p.write(t, "target/.gitignore", "custom git exclusions\n")
	p.write(t, "target/docs/guide.md", "# Guide\nExample project.\n")
	p.write(t, "target/.github/workflows/agent.lock.yml", "compiledonly: generated\n")
	p.write(t, "target/.github/workflows/keep.lock.yml", "retainedonly: generated\n")
	original := strings.Replace(assets.KWBIgnore(), ".github/workflows/*.lock.yml", "obsolete", 1) +
		"custom/\n!.github/workflows/keep.lock.yml\n"
	p.write(t, "target/.go42x/kwb.ignore", original)

	output := p.run(t, 0, "kwb", "build", "--root=target", "--json", "--log-level=error")
	var report kwb.BuildResult
	if err := json.Unmarshal([]byte(output.stdout), &report); err != nil || report.Indexed == 0 ||
		len(report.BackupDir) == 0 || output.stderr != "" {
		t.Fatalf("JSON build = %+v, %+v, %v", output, report, err)
	}
	backup, err := os.ReadFile(filepath.Join(report.BackupDir, "files", ".go42x", "kwb.ignore"))
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if got := p.read(
		t,
		"target/.go42x/kwb.ignore",
	); got != assets.KWBIgnore()+"custom/\n!.github/workflows/keep.lock.yml\n" {
		t.Fatalf("custom rules or header were changed: %q", got)
	}
	for path, want := range map[string]string{
		".go42x/kwb.ignore": "caller exclusions\n",
		"target/.gitignore": "custom git exclusions\n",
	} {
		if got := p.read(t, path); got != want {
			t.Errorf("build changed %s: %q", path, got)
		}
	}
	if _, err := os.Stat(filepath.Join(p.root, "target", ".go42x", "go42x.yaml")); !os.IsNotExist(err) {
		t.Fatalf("build created agent configuration: %v", err)
	}
	for query, total := range map[string]uint64{"compiledonly": 0, "retainedonly": 1} {
		output := p.run(t, 0, "kwb", "search", query, "--root=target", "--json")
		var result kwb.SearchResponse
		if err := json.Unmarshal([]byte(output.stdout), &result); err != nil || result.Total != total {
			t.Fatalf("search %s = %+v, %v", query, result, err)
		}
	}

	before := p.snapshot(t)
	unchanged := p.run(t, 0, "kwb", "build", "--root=target", "--json")
	var repeated kwb.BuildResult
	if err := json.Unmarshal(
		[]byte(unchanged.stdout),
		&repeated,
	); err != nil || repeated.Indexed != 0 ||
		len(repeated.BackupDir) > 0 {
		t.Fatalf("repeated build = %+v, %v", repeated, err)
	}
	p.assertUnchanged(t, before)
}

func TestKnowledgeBaseBuildRejectsInvalidExclusions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, path, content string }{
		{"unmatched marker", ".go42x/kwb.ignore", "# go42x -- START\ncustom/\n"},
		{"duplicate blocks", ".go42x/kwb.ignore", assets.KWBIgnore() + assets.KWBIgnore()},
		{"backup failure", ".go42x/backups/updates", "blocked"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newProject(t)
			p.write(t, "guide.md", "# Guide\nOriginal project.\n")
			p.run(t, 0, "kwb", "build")
			p.write(t, ".go42x/kwb.ignore", "custom/\n")
			p.write(t, tt.path, tt.content)
			before := p.snapshot(t)

			output := p.run(t, 1, "kwb", "build", "--json")
			var report struct {
				Error  string           `json:"error"`
				Result *kwb.BuildResult `json:"result"`
			}
			if err := json.Unmarshal(
				[]byte(output.stdout),
				&report,
			); err != nil || len(report.Error) == 0 ||
				report.Result == nil ||
				len(report.Result.BackupDir) > 0 {
				t.Fatalf("build error = %+v, %v", output, err)
			}
			p.assertUnchanged(t, before)
		})
	}
}

func TestKnowledgeBaseFailedBuildReportsExclusionsBackup(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, "guide.md", "# Guide\nOriginal project.\n")
	p.run(t, 0, "kwb", "build")
	original := strings.Replace(assets.KWBIgnore(), ".github/workflows/*.lock.yml", "obsolete", 1)
	p.write(t, ".go42x/kwb.ignore", original)
	if err := os.MkdirAll(filepath.Join(p.root, "broken", ".gitignore"), 0755); err != nil {
		t.Fatal(err)
	}
	published := p.read(t, ".go42x/kwb/index/CURRENT")

	output := p.run(t, 1, "kwb", "build", "--json", "--log-level=error")
	var report struct {
		Error  string           `json:"error"`
		Result *kwb.BuildResult `json:"result"`
	}
	if err := json.Unmarshal(
		[]byte(output.stdout),
		&report,
	); err != nil || len(report.Error) == 0 ||
		report.Result == nil ||
		len(report.Result.BackupDir) == 0 {
		t.Fatalf("partial build omitted the backup: %+v, %v", output, err)
	}
	backup, err := os.ReadFile(filepath.Join(report.Result.BackupDir, "files", ".go42x", "kwb.ignore"))
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if got := p.read(t, ".go42x/kwb/index/CURRENT"); got != published {
		t.Fatalf("failed build changed the published index: %q", got)
	}

	p.write(t, ".go42x/kwb.ignore", original)
	text := p.run(t, 1, "kwb", "build", "--log-level=error")
	if !strings.HasPrefix(text.stdout, "Backup: ") || strings.Contains(text.stdout, "Knowledge base updated") {
		t.Fatalf("failed build omitted text recovery information: %+v", text)
	}
}

func TestKnowledgeBaseReadsLeaveExclusionsUnchanged(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, "guide.md", "# Guide\nOriginal project.\n")
	p.run(t, 0, "kwb", "build")
	p.write(t, ".go42x/kwb.ignore", "# go42x -- START\ncustom/\n")
	before := p.snapshot(t)

	p.run(t, 0, "kwb", "stats", "--json")
	p.run(t, 0, "kwb", "search", "Original", "--json")
	p.run(t, 1, "kwb", "check", "--json")
	p.assertUnchanged(t, before)
}
