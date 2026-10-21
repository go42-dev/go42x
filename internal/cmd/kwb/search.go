package kwb

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/adapters/cli"
)

func newSearchCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search indexed source and documentation",
		Long: `Search the last completed index using a plain-text query.
Run 'go42x kwb build' to include recent file changes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := &kwb.SearchSettings{
				KnowledgeBase: queryKnowledgeBaseSettings(cmd),
				Search: kwb.SearchOptions{
					Query:      args[0],
					Kind:       viper.GetString(flagKind),
					Language:   viper.GetString(flagLanguage),
					PathPrefix: viper.GetString(flagPathPrefix),
					Limit:      viper.GetInt(flagLimit),
					Offset:     viper.GetInt(flagOffset),
				},
				JSON: viper.GetBool(flagJSON),
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runSearchCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		flagJSON, false,
		"print structured search results")
	cmd.Flags().String(
		flagKind, "",
		"file kind: code, documentation, config")
	cmd.Flags().String(
		flagLanguage, "",
		"language or format, such as go or md")
	cmd.Flags().String(
		flagPathPrefix, "",
		"project-relative path prefix")
	cmd.Flags().Int(
		flagLimit, kwb.NewSettings().SearchLimit,
		"maximum results")
	cmd.Flags().Int(
		flagOffset, 0,
		"result offset; use next_offset from the previous response")

	return cmd
}

func runSearchCommand(f *cmdutil.Factory, settings *kwb.SearchSettings) error {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportSearch(f.Output(), settings, nil, err)
	}

	result, err := service.Search(f.Context(), settings.Search)
	err = errors.Join(err, service.Close())
	return cli.ReportSearch(f.Output(), settings, result, err)
}
