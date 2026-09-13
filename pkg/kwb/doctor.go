package kwb

import (
	"context"
	"path/filepath"

	"github.com/go42-dev/go42x/pkg/doctor/check"
)

// DoctorChecks supplies knowledge-base diagnostics for registration with doctor.Service.
func DoctorChecks(root, indexPath string) []check.Check {
	if indexPath == "" {
		indexPath = filepath.Join(root, NewSettings().IndexPath)
	}
	return []check.Check{{ID: "kwb.index", Run: func(ctx context.Context) check.Result {
		settings := NewSettings()
		settings.RootPath = root
		settings.IndexPath = indexPath
		settings.RequireRootMatch = true
		service, err := NewService(settings)
		if err != nil {
			return check.Result{
				Status:  check.Fail,
				Message: "Invalid knowledge-base settings",
			}
		}
		defer service.Close() //nolint:errcheck
		stats, err := service.GetStats(ctx)
		if err != nil {
			return check.Result{
				Status:      check.Fail,
				Message:     "Index missing, unreadable, incompatible, or belongs to another project",
				Remediation: "go42x kwb build --rebuild",
			}
		}
		return check.Result{
			Status:   check.Pass,
			Message:  "Index is readable and matches the project; source freshness is unchecked",
			Evidence: []string{stats.Generation},
		}
	}}}
}
