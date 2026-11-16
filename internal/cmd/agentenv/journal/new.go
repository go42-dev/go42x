package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newNewCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a journal entry for a task",
		Long: `Create an open entry with an entry.json record and a notes.md template for handoff notes.
Before creating one, list open entries and continue the entry that matches the task.`,
		Example: `  go42x agentenv journal new --title "Move schemas into the bundle" --tag refactor --tag agentenv`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := newSettings(f, true)
			options := journal.NewOptions{
				Title: viper.GetString(flagTitle),
				Tags:  viper.GetStringSlice(flagTag),
				PR:    viper.GetString(flagPR),
				Issue: viper.GetString(flagIssue),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runNewCommand(f, settings, options)
		},
	}

	cmd.Flags().String(
		flagTitle, "",
		"what the task is about (required)")
	cmd.Flags().StringSlice(
		flagTag, nil,
		"one type of work and the affected areas; repeat the flag or separate tags with commas")
	cmd.Flags().String(
		flagPR, "",
		"pull request number or URL")
	cmd.Flags().String(
		flagIssue, "",
		"issue number or URL")
	addActorFlags(cmd)
	return cmd
}

func runNewCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.NewOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportNew(f.Output(), settings, nil, err)
	}

	result, err := service.New(f.Context(), settings.Actor, options)
	return cli.ReportNew(f.Output(), settings, result, err)
}
