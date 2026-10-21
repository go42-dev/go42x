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

func newStatsCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show knowledge-base index statistics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &kwb.StatsSettings{
				KnowledgeBase: queryKnowledgeBaseSettings(cmd),
				JSON:          viper.GetBool(flagJSON),
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runStatsCommand(f, settings)
		},
	}
	cmd.Flags().Bool(
		flagJSON, false,
		"print structured index statistics")
	return cmd
}

func runStatsCommand(f *cmdutil.Factory, settings *kwb.StatsSettings) error {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportStats(f.Output(), settings, nil, err)
	}

	result, err := service.GetStats(f.Context())
	err = errors.Join(err, service.Close())
	return cli.ReportStats(f.Output(), settings, result, err)
}
