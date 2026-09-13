package kwb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go42-dev/go42x/pkg/doctor/check"
)

func TestDoctorIndexCheck(t *testing.T) {
	root := t.TempDir()
	report, err := check.Run(t.Context(), DoctorChecks(root, "")...)
	if err != nil || len(report.Checks) != 1 || report.Checks[0].ID != "kwb.index" || report.Status != check.Fail {
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
		{"default index", root, "", check.Pass},
		{"explicit index", root, settings.IndexPath, check.Pass},
		{"other checkout", t.TempDir(), settings.IndexPath, check.Fail},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := check.Run(t.Context(), DoctorChecks(test.root, test.indexPath)...)
			if err != nil || report.Status != test.status {
				t.Fatalf("index report: %+v %v", report, err)
			}
		})
	}
}
