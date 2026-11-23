package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newNoteCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note ID",
		Short: "Record an update to a journal entry",
		Long: `Record that the entry's notes.md changed, with an optional summary for its log.
--keep keeps the entry, so journal gc never deletes it; --keep=false lets gc delete it again.`,
		Example: `  go42x agentenv journal note 20261110-move-schemas-into-the-bundle-3f2a --summary "Moved the schemas"
  go42x agentenv journal note 20261110-move-schemas-into-the-bundle-3f2a --keep`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := newSettings(f, true)
			options := journal.NoteOptions{
				ID:      args[0],
				Summary: viper.GetString(flagSummary),
				PR:      viper.GetString(flagPR),
				Issue:   viper.GetString(flagIssue),
			}
			if cmdutil.ExplicitFlag(cmd, flagKeep) {
				options.Keep = new(viper.GetBool(flagKeep))
			}
			if cmdutil.ExplicitFlag(cmd, flagTag) {
				options.Tags = viper.GetStringSlice(flagTag)
				if options.Tags == nil {
					options.Tags = []string{}
				}
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runNoteCommand(f, settings, options)
		},
	}

	cmd.Flags().String(
		flagSummary, "",
		"what changed, for the entry's log")
	cmd.Flags().StringSlice(
		flagTag, nil,
		"replace the entry's tags")
	cmd.Flags().String(
		flagPR, "",
		"pull request number or URL")
	cmd.Flags().String(
		flagIssue, "",
		"issue number or URL")
	cmd.Flags().Bool(
		flagKeep, false,
		"keep the entry, so journal gc never deletes it; --keep=false lets gc delete it again")
	addActorFlags(cmd)
	return cmd
}

func runNoteCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.NoteOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportNote(f.Output(), settings, nil, err)
	}

	result, err := service.Note(f.Context(), settings.Actor, options)
	return cli.ReportNote(f.Output(), settings, result, err)
}
