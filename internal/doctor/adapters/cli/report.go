package cli

import (
	"fmt"
	"io"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/doctor"
	"github.com/go42-dev/go42x/internal/doctor/check"
)

func Report(output io.Writer, settings *doctor.Settings, result *check.Report, runErr error) error {
	if runErr == nil && result.Status == check.StatusFail {
		runErr = cmdutil.ReportedError(fmt.Errorf("doctor found failed checks"))
	}
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result == nil {
				return nil
			}

			return printChecks(w, result.Checks, "")
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func printChecks(output io.Writer, checks []check.Result, indent string) error {
	for _, item := range checks {
		if _, err := fmt.Fprintf(output, "%s%s %s: %s\n", indent, item.Status, item.ID, item.Message); err != nil {
			return err
		}
		if len(item.Remediation) > 0 {
			if _, err := fmt.Fprintf(output, "%s  %s\n", indent, item.Remediation); err != nil {
				return err
			}
		}
		for _, evidence := range item.Evidence {
			if _, err := fmt.Fprintf(output, "%s  %s\n", indent, evidence); err != nil {
				return err
			}
		}
		if err := printChecks(output, item.Children, indent+"  "); err != nil {
			return err
		}
	}
	return nil
}
