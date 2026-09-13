package cmd

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/doctor"
	"github.com/go42-dev/go42x/pkg/doctor/check"
)

func NewDoctorCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := doctor.NewSettings()
	cmd := &cobra.Command{
		Use:         "doctor",
		Short:       "Inspect configuration, generated outputs, MCP servers, and the index",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"result-output": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &doctor.Settings{
				RootPath:  viper.GetString("root"),
				IndexPath: viper.GetString("index"),
				ProbeMCP:  viper.GetBool("probe-mcp"),
				Timeout:   viper.GetDuration("timeout"),
				JSON:      viper.GetBool("json"),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runDoctorCommand(f, settings)
		},
	}

	cmd.Flags().Bool("json", false, "print structured diagnostics")
	cmd.Flags().String("root", defaults.RootPath, "project root to inspect")
	cmd.Flags().Bool("probe-mcp", false, "start/connect to enabled servers, initialize, and list tools (no tool calls)")
	cmd.Flags().Duration("timeout", defaults.Timeout, "maximum duration of each MCP probe")
	cmd.Flags().String("index", "", "knowledge-base index path (default .go42x/kwb/index)")

	return cmd
}

func runDoctorCommand(f *cmdutil.Factory, settings *doctor.Settings) error {
	service, err := doctor.NewService(settings, doctor.WithLogger(slog.Default().With("component", "doctor-service")))
	if err != nil {
		return err
	}
	report, err := service.Run(f.Context())
	if err != nil {
		return err
	}
	if settings.JSON {
		err = cmdutil.JSON(f.Output(), report)
	} else {
		err = printChecks(f.Output(), report.Checks, "")
	}
	if err != nil {
		return err
	}
	if report.Status == check.Fail {
		return cmdutil.FailedChecks()
	}
	return nil
}

func printChecks(output io.Writer, checks []check.Result, indent string) error {
	for _, check := range checks {
		if _, err := fmt.Fprintf(
			output,
			"%s%s %s: %s\n",
			indent,
			check.Status,
			check.ID,
			check.Message,
		); err != nil {
			return err
		}
		if check.Remediation != "" {
			if _, err := fmt.Fprintf(output, "%s  %s\n", indent, check.Remediation); err != nil {
				return err
			}
		}
		for _, evidence := range check.Evidence {
			if _, err := fmt.Fprintf(output, "%s  %s\n", indent, evidence); err != nil {
				return err
			}
		}
		if err := printChecks(output, check.Children, indent+"  "); err != nil {
			return err
		}
	}
	return nil
}
