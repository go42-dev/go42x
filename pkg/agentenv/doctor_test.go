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

	"github.com/go42-dev/go42x/pkg/doctor/check"
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
	if err != nil || report.Status != check.Fail || report.Checks[0].Status != check.Fail {
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
	if err != nil || report.Status != check.Warn || report.Checks[1].Status != check.Warn {
		t.Fatalf("%+v %v", report, err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("drift doctor wrote files")
	}
	if err = service.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, root)
	report, err = check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Checks[1].Status != check.Pass {
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
		"{{.environment.working_dir}} {{.golang.go_version}}\n",
	)
	writeFile(t, filepath.Join(root, "go.mod"), "module example\ngo 1.27\n")
	if err := service.Generate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	report, err := check.Run(t.Context(), service.DoctorChecks(root)...)
	if err != nil || report.Checks[1].Status != check.Pass {
		t.Fatalf("wrong checkout context: %+v %v", report, err)
	}
}
