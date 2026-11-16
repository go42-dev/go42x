package journal

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newShowCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "show ID",
		Short:   "Show a journal entry with its notes, log, and files",
		Example: "  go42x agentenv journal show 20261110-move-schemas-into-the-bundle-3f2a",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := newSettings(f, false)
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := journal.ValidateID(args[0]); err != nil {
				return cmdutil.UsageError(err)
			}
			return runShowCommand(f, settings, args[0])
		},
	}
	return cmd
}

func runShowCommand(f *cmdutil.Factory, settings *journal.Settings, id string) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportShow(f.Output(), settings, nil, err)
	}

	result, err := service.Show(f.Context(), id)
	return cli.ReportShow(f.Output(), settings, result, err)
}
