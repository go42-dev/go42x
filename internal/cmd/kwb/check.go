package kwb

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/adapters/cli"
)

func newCheckCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check index freshness without updating it",
		Long: `Compare the index with current project files without updating it.
Exit with status 1 unless a complete scan confirms the index is fresh.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &kwb.CheckSettings{
				KnowledgeBase: queryKnowledgeBaseSettings(cmd),
				JSON:          viper.GetBool(flagJSON),
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runCheckCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		flagJSON, false,
		"output structured freshness results")

	return cmd
}

func runCheckCommand(f *cmdutil.Factory, settings *kwb.CheckSettings) error {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportCheck(f.Output(), settings, nil, err)
	}

	result, err := service.CheckFreshness(f.Context())

	if closeErr := service.Close(); closeErr != nil {
		// A close failure is not described by the freshness report.
		return cli.ReportCheck(f.Output(), settings, nil, errors.Join(err, closeErr))
	}

	return cli.ReportCheck(f.Output(), settings, result, err)
}
