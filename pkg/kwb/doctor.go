package kwb

import (
	"context"
	"fmt"
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
		freshness, err := service.CheckFreshness(ctx)
		if err != nil {
			if freshness != nil && freshness.Generation != "" {
				return check.Result{
					Status:      check.Warn,
					Message:     "Index freshness scan could not complete",
					Remediation: "go42x kwb check --json --search-timeout=30s",
					Evidence:    []string{freshness.Generation},
				}
			}
			return check.Result{
				Status:      check.Fail,
				Message:     "Index missing, unreadable, incompatible, or belongs to another project",
				Remediation: "go42x kwb build --rebuild",
			}
		}
		if !freshness.Complete || freshness.Status != "fresh" {
			return check.Result{Status: check.Warn,
				Message: fmt.Sprintf("Index %s: %d new, %d changed, %d deleted, %d excluded", freshness.Status,
					freshness.New.Count, freshness.Changed.Count, freshness.Deleted.Count, freshness.Excluded.Count),
				Remediation: "go42x kwb build; go42x kwb check --json", Evidence: []string{freshness.Generation}}
		}
		return check.Result{
			Status:   check.Pass,
			Message:  "Index matches eligible source checked during this scan",
			Evidence: []string{freshness.Generation},
		}
	}}}
}
