package cli

import (
	"fmt"
	"io"

	"github.com/go42-dev/go42x/internal/agentenv"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func ReportInit(output io.Writer, settings *agentenv.Settings, result *agentenv.InitResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result != nil && result.UpdateResult != nil && len(result.BackupDir) > 0 {
				if _, err := fmt.Fprintf(w, "Backup: %s\n", result.BackupDir); err != nil {
					return err
				}
			}
			if runErr != nil {
				return nil
			}
			if result.UpdateResult != nil {
				_, err := fmt.Fprintf(w,
					"Reinstalled %d source files in %s. Run go42x agentenv generate to refresh generated outputs.\n",
					result.Sources, result.Directory)
				return err
			}

			_, err := fmt.Fprintf(w, "Initialized agentenv in %s.\n", result.Directory)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportGenerate(
	output io.Writer,
	settings *agentenv.Settings,
	result *agentenv.GenerateResult,
	runErr error,
) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if runErr != nil {
				return nil
			}

			_, err := fmt.Fprintf(w, "Generated agentenv; %d file changes applied.\n", len(result.Changes))
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}

func ReportUpdate(output io.Writer, settings *agentenv.Settings, result *agentenv.UpdateResult, runErr error) error {
	var text func(io.Writer) error
	if !settings.JSON {
		text = func(w io.Writer) error {
			if result != nil && len(result.BackupDir) > 0 {
				if _, err := fmt.Fprintf(w, "Backup: %s\n", result.BackupDir); err != nil {
					return err
				}
			}
			if runErr != nil {
				return nil
			}
			for _, path := range result.Overwritten {
				if _, err := fmt.Fprintf(
					w,
					"Replaced local changes in %s; the backup keeps the original.\n",
					path,
				); err != nil {
					return err
				}
			}

			_, err := fmt.Fprintf(w,
				"Updated %d source files. Run go42x agentenv generate to refresh generated outputs.\n", result.Sources)
			return err
		}
	}
	return cmdutil.Report(output, result, runErr, text)
}
