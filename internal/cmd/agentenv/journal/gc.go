package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newGCCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete journal entries that went without an update for the retention period",
		Long: `Report entries that went without an update for journal.retention-days, 30 by default;
--apply deletes them. Kept entries and content without a journal record are never deleted.`,
		Example: `  go42x agentenv journal gc
  go42x agentenv journal gc --apply`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := newSettings(f, false)
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runGCCommand(f, settings, journal.GCOptions{Apply: viper.GetBool(flagApply)})
		},
	}

	cmd.Flags().Bool(
		flagApply, false,
		"delete the reported entries")
	return cmd
}

func runGCCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.GCOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportGC(f.Output(), settings, nil, err)
	}

	result, err := service.GC(f.Context(), options)
	return cli.ReportGC(f.Output(), settings, result, err)
}
