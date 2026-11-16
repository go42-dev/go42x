package kwb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/doctor/check"
)

func TestDoctorIndexCheck(t *testing.T) {
	root := t.TempDir()
	report, err := check.Run(t.Context(), DoctorChecks(root, "")...)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].ID != "kwb.index" ||
		report.Status != check.StatusFail {
		t.Fatalf("missing index: %+v %v", report, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing-index check wrote files: %+v %v", entries, err)
	}
	settings := NewSettings()
	settings.RootPath, settings.IndexPath = root, filepath.Join(root, settings.IndexPath)
	service, err := NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		root      string
		indexPath string
		status    check.Status
	}{
		{
			"default index",
			root,
			"",
			check.StatusPass,
		},
		{
			"explicit index",
			root,
			settings.IndexPath,
			check.StatusPass,
		},
		{
			"other checkout",
			t.TempDir(),
			settings.IndexPath,
			check.StatusFail,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := check.Run(t.Context(), DoctorChecks(test.root, test.indexPath)...)
			if err != nil || report.Status != test.status {
				t.Fatalf("index report: %+v %v", report, err)
			}
		})
	}
	writeFixture(t, root, "new.md", "new source\n")
	report, err = check.Run(t.Context(), DoctorChecks(root, "")...)
	if err != nil || report.Status != check.StatusWarn {
		t.Fatalf("stale index: %+v %v", report, err)
	}
}

func TestDoctorIndexCheckWithoutRecordedSettings(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "guide.md", "# Guide\n")
	settings := NewSettings()
	settings.RootPath, settings.IndexPath = root, filepath.Join(root, settings.IndexPath)
	service, err := NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BuildIndex(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	// An index written without its build settings cannot be checked for freshness.
	current, err := os.ReadFile(filepath.Join(settings.IndexPath, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(settings.IndexPath, strings.TrimSpace(string(current)), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "selection")
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	report, err := check.Run(t.Context(), DoctorChecks(root, "")...)
	if err != nil || report.Status != check.StatusWarn || len(report.Checks) != 1 {
		t.Fatalf("index without settings: %+v %v", report, err)
	}
	result := report.Checks[0]
	if result.Remediation != "go42x kwb build" ||
		!strings.Contains(strings.Join(result.Evidence, "\n"), "build settings") {
		t.Errorf("index without settings = %+v", result)
	}
}
