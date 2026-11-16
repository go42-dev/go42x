package journal

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newIndexCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Rebuild the generated journal index",
		Long: `Rebuild .go42x/journal/.index.json from the entry records. Every journal change
refreshes the index; rebuild it after editing or removing entries by hand.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := newSettings(f, false)
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runIndexCommand(f, settings)
		},
	}
	return cmd
}

func runIndexCommand(f *cmdutil.Factory, settings *journal.Settings) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportIndex(f.Output(), settings, nil, err)
	}

	result, err := service.Index(f.Context())
	return cli.ReportIndex(f.Output(), settings, result, err)
}
