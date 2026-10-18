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
					Kind:       viper.GetString("kind"),
					Language:   viper.GetString("language"),
					PathPrefix: viper.GetString("path-prefix"),
					Limit:      viper.GetInt("limit"),
					Offset:     viper.GetInt("offset"),
				},
				JSON: viper.GetBool("json"),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runSearchCommand(f, settings)
		},
	}

	cmd.Flags().Bool("json", false, "print structured search results")
	cmd.Flags().String("kind", "", "file kind: code, documentation, config")
	cmd.Flags().String("language", "", "language or format, such as go or md")
	cmd.Flags().String("path-prefix", "", "project-relative path prefix")
	cmd.Flags().Int("limit", kwb.NewSettings().SearchLimit, "maximum results")
	cmd.Flags().Int("offset", 0, "result offset; use next_offset from the previous response")

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
