package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newSearchCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "search QUERY",
		Short:   "Search journal entries by title, ID, tags, and notes",
		Example: `  go42x agentenv journal search "schema migration"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := newSettings(f, false)
			options := journal.SearchOptions{
				Query: args[0],
				Limit: viper.GetInt(flagLimit),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runSearchCommand(f, settings, options)
		},
	}

	cmd.Flags().Int(
		flagLimit, 20,
		"maximum entries; 0 returns all")
	return cmd
}

func runSearchCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.SearchOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportSearch(f.Output(), settings, nil, err)
	}

	result, err := service.Search(f.Context(), options)
	return cli.ReportSearch(f.Output(), settings, result, err)
}
