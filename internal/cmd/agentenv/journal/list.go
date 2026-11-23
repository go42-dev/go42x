package journal

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	"github.com/go42-dev/go42x/internal/agentenv/journal/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newListCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List journal entries, most recently updated first",
		Long: `List journal entries that match every filter. --issue and --pr match the recorded
reference; a number, with or without "#", also matches a URL that ends with it. With --stray,
list journal content that no entry describes. go42x never deletes it;
review it and delete what is no longer needed. Agents ask the user first.`,
		Example: `  go42x agentenv journal list --tag refactor
  go42x agentenv journal list --issue https://github.com/go42-dev/go42x/issues/42
  go42x agentenv journal list --since 2026-11-10T09:00:00Z
  go42x agentenv journal list --stray`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := newSettings(f, false)
			since, err := journal.ParseSince(viper.GetString(flagSince))
			if err != nil {
				return cmdutil.UsageError(err)
			}
			options := journal.ListOptions{
				Tags:  viper.GetStringSlice(flagTag),
				Since: since,
				Kept:  viper.GetBool(flagKept),
				PR:    viper.GetString(flagPR),
				Issue: viper.GetString(flagIssue),
				Stray: viper.GetBool(flagStray),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runListCommand(f, settings, options)
		},
	}

	cmd.Flags().StringSlice(
		flagTag, nil,
		"only entries with every listed tag")
	cmd.Flags().String(
		flagSince, "",
		"only entries updated at or after this RFC 3339 time")
	cmd.Flags().Bool(
		flagKept, false,
		"only kept entries, which journal gc never deletes")
	cmd.Flags().String(
		flagPR, "",
		"only entries for this pull request number or URL")
	cmd.Flags().String(
		flagIssue, "",
		"only entries for this issue number or URL")
	cmd.Flags().Bool(
		flagStray, false,
		"list content without a journal record instead of entries")

	return cmd
}

func runListCommand(f *cmdutil.Factory, settings *journal.Settings, options journal.ListOptions) error {
	service, err := journal.NewService(settings, journal.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportList(f.Output(), settings, nil, options.Stray, err)
	}

	result, err := service.List(f.Context(), options)
	return cli.ReportList(f.Output(), settings, result, options.Stray, err)
}
