package agentenv

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/doctor/check"
)

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		result[path] = fmt.Sprintf("%s %d %s", info.Mode(), info.ModTime().UnixNano(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDoctorMissingAndDriftDoNotWrite(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, false)
	before := snapshotTree(t, root)
	report, err := check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Status != check.StatusFail || report.Checks[0].Status != check.StatusFail {
		t.Fatalf("%+v %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("missing config doctor wrote files")
	}
	writeFile(
		t,
		filepath.Join(root, ".go42x/go42x.yaml"),
		"version: '1.0'\nproject: {name: test}\ncontext: {template: agents.tpl.md}\nproviders:\n  codex: {enabled: false}\n",
	)
	writeFile(t, filepath.Join(root, ".go42x/agents.tpl.md"), "# Test\n")
	before = snapshotTree(t, root)
	report, err = check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Status != check.StatusWarn || report.Checks[1].Status != check.StatusWarn {
		t.Fatalf("%+v %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("drift doctor wrote files")
	}
	if _, err = service.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, root)
	report, err = check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Checks[1].Status != check.StatusPass {
		t.Fatalf("%+v %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("healthy doctor wrote files")
	}
}

func TestDoctorRedactsInvalidConfiguration(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, false)
	writeFile(t, filepath.Join(root, ".go42x/go42x.yaml"), "env: secret-do-not-report\n")
	report, err := check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-do-not-report") {
		t.Fatal("configuration secret leaked")
	}
}

func TestDoctorGHAWFragmentDrift(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, false)
	writeFile(t, filepath.Join(root, ".go42x/go42x.yaml"),
		"version: '1.0'\nproject: {name: test}\ncontext: {template: agents.tpl.md}\n"+
			"providers: {gh-aw: {enabled: true}}\nmcp: {local: {enabled: true, name: local, command: npx}}\n")
	writeFile(t, filepath.Join(root, ".go42x/agents.tpl.md"), "# Test\n")
	if _, err := service.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)

	report, err := check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Status != check.StatusPass {
		t.Fatalf("generated fragment check = %+v, %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("doctor changed generated files")
	}

	writeFile(t, filepath.Join(root, ".github/workflows/900-aw-mcp.md"), "outdated declarations\n")
	before = snapshotTree(t, root)
	report, err = check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Checks[1].Status != check.StatusWarn ||
		!strings.Contains(strings.Join(report.Checks[1].Evidence, "\n"), ".github/workflows/900-aw-mcp.md") {
		t.Fatalf("fragment drift check = %+v, %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("doctor repaired drift without authorization")
	}
}

func TestDoctorChecksUseExplicitCheckout(t *testing.T) {
	root := t.TempDir()
	service := testService(t, root, false)
	writeFile(
		t,
		filepath.Join(root, ".go42x/go42x.yaml"),
		"version: '1.0'\nproject: {name: target}\ncontext: {template: agents.tpl.md}\nproviders: {codex: {enabled: false}}\n",
	)
	writeFile(
		t,
		filepath.Join(root, ".go42x/agents.tpl.md"),
		"# Instructions\n\n{{.environment.working_dir}} {{.golang.go_version}}\n",
	)
	writeFile(t, filepath.Join(root, "go.mod"), "module example\ngo 1.27\n")
	if _, err := service.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	report, err := check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Checks[1].Status != check.StatusPass {
		t.Fatalf("wrong checkout context: %+v %v", report, err)
	}
}
