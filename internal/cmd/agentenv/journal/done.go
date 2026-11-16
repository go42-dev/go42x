package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newDoneCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done ID",
		Short: "Finish a journal entry",
		Long: `Finish an open entry. journal gc may delete a finished entry 30 days later, or
7 days after it is abandoned. Use --keep for an entry that gc never deletes.`,
		Example: `  go42x agentenv journal done 20261110-move-schemas-into-the-bundle-3f2a
  go42x agentenv journal done 20261110-move-schemas-into-the-bundle-3f2a --abandoned`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := newSettings(f, true)
			options := journal.DoneOptions{
				ID:        args[0],
				Summary:   viper.GetString(flagSummary),
				Abandoned: viper.GetBool(flagAbandoned),
				Keep:      viper.GetBool(flagKeep),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runDoneCommand(f, settings, options)
		},
	}

	cmd.Flags().String(
		flagSummary, "",
		"outcome, for the entry's log")
	cmd.Flags().Bool(
		flagAbandoned, false,
		"mark the task as dropped without finishing it")
	cmd.Flags().Bool(
		flagKeep, false,
		"finish without an expiry, so journal gc never deletes the entry")
	addActorFlags(cmd)
	return cmd
}

func runDoneCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.DoneOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportDone(f.Output(), settings, nil, err)
	}

	result, err := service.Done(f.Context(), settings.Actor, options)
	return cli.ReportDone(f.Output(), settings, result, err)
}
