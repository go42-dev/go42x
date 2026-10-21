package cli

import (
	"fmt"
	"io"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/fspatch"
	"github.com/go42-dev/go42x/internal/vendoring"
)

// Report writes a vendoring result and returns its CLI exit status.
func Report(
	output io.Writer,
	settings *vendoring.Settings,
	operation string,
	result *vendoring.Result,
	runErr error,
) error {
	if result == nil {
		result = &vendoring.Result{
			Operation: operation,
			Changes:   []fspatch.Change{},
			Issues:    []vendoring.Issue{},
		}
	}
	if runErr != nil {
		result.Valid = false
		result.Issues = append(result.Issues, vendoring.Issue{Reason: runErr.Error()})
	}

	var failure error
	if !result.Valid {
		failure = cmdutil.ReportedError(fmt.Errorf("vendoring %s failed", result.Operation))
	}
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			return writeText(w, result)
		}
	}
	return cmdutil.Report(output, result, failure, text)
}

func writeText(output io.Writer, result *vendoring.Result) error {
	status := "complete"
	if !result.Valid {
		status = "failed"
	} else if result.DryRun {
		status = "dry-run"
	}

	if _, err := fmt.Fprintf(
		output,
		"%s: %s; %d checked, %d planned, %d applied\n",
		result.Operation,
		status,
		result.Checked,
		len(result.Changes),
		result.Applied,
	); err != nil {
		return err
	}

	for _, issue := range result.Issues {
		if _, err := fmt.Fprintf(output, "%s: %s\n", issue.Path, issue.Reason); err != nil {
			return err
		}
	}
	if len(result.BackupDir) > 0 {
		_, err := fmt.Fprintln(output, "Recovery backup:", result.BackupDir)
		return err
	}

	return nil
}
