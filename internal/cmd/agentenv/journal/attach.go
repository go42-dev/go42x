package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newAttachCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach ID FILE",
		Short: "Copy a file from the project into a journal entry",
		Long: `Copy a file from the project, such as a report or a log, into the entry's directory.
An existing file in the entry is never replaced. Record the change with journal note.`,
		Example: `  go42x agentenv journal attach 20261110-move-schemas-into-the-bundle-3f2a review.md
  go42x agentenv journal attach 20261110-move-schemas-into-the-bundle-3f2a review.md --name review-2026-11-10.md`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := newSettings(f, false)
			options := journal.AttachOptions{
				ID:   args[0],
				Path: args[1],
				Name: viper.GetString(flagName),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runAttachCommand(f, settings, options)
		},
	}

	cmd.Flags().String(
		flagName, "",
		"file name of the copy in the entry; the file's own name when empty")

	return cmd
}

func runAttachCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.AttachOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportAttach(f.Output(), settings, nil, err)
	}

	result, err := service.Attach(f.Context(), options)
	return cli.ReportAttach(f.Output(), settings, result, err)
}
