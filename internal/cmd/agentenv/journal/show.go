package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newShowCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Show a journal entry with its notes, log, and files",
		Example: `  go42x agentenv journal show 20261110-move-schemas-into-the-bundle-3f2a
  go42x agentenv journal show 20261110-move-schemas-into-the-bundle-3f2a --sections --since 2026-11-10T09:00:00Z`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := newSettings(f, false)
			since, err := journal.ParseSince(viper.GetString(flagSince))
			if err != nil {
				return cmdutil.UsageError(err)
			}
			options := journal.ShowOptions{
				ID:       args[0],
				Since:    since,
				Sections: viper.GetBool(flagSections),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runShowCommand(f, settings, options)
		},
	}

	cmd.Flags().String(
		flagSince, "",
		"only log events at or after this RFC 3339 time")
	cmd.Flags().Bool(
		flagSections, false,
		"split the notes by section, without the template's comments and empty sections")

	return cmd
}

func runShowCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.ShowOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportShow(f.Output(), settings, nil, err)
	}

	result, err := service.Show(f.Context(), options)
	return cli.ReportShow(f.Output(), settings, result, err)
}
