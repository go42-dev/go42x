package kwb

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newSearchCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "search QUERY", Short: "Search indexed source and documentation", Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"result-output": "true"},
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

	cmd.Flags().Bool("json", false, "print the same structured result as the MCP tool")
	cmd.Flags().String("kind", "", "file kind: code, documentation, config")
	cmd.Flags().String("language", "", "language or format, such as go or md")
	cmd.Flags().String("path-prefix", "", "project-relative path prefix")
	cmd.Flags().Int("limit", kwb.NewSettings().SearchLimit, "maximum results")
	cmd.Flags().Int("offset", 0, "result offset; use next_offset from the previous response")

	return cmd
}

func newReadCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "read PATH", Short: "Read current project source lines", Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"result-output": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := &kwb.ReadSettings{
				KnowledgeBase: queryKnowledgeBaseSettings(cmd),
				Path:          args[0],
				StartLine:     viper.GetInt("start-line"),
				EndLine:       viper.GetInt("end-line"),
				JSON:          viper.GetBool("json"),
			}
			if !cmdutil.ExplicitFlag(cmd, "end-line") {
				settings.EndLine = settings.StartLine + 199
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runReadCommand(f, settings)
		},
	}

	cmd.Flags().Bool("json", false, "print the same structured result as the MCP tool")
	cmd.Flags().Int("start-line", 1, "first line, one-based")
	cmd.Flags().Int("end-line", 0, "last line, inclusive (default start-line + 199)")

	return cmd
}

func newStatsCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "stats", Short: "Show index counts and project root", Args: cobra.NoArgs,
		Annotations: map[string]string{"result-output": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &kwb.StatsSettings{KnowledgeBase: queryKnowledgeBaseSettings(cmd), JSON: viper.GetBool("json")}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runStatsCommand(f, settings)
		},
	}
	cmd.Flags().Bool("json", false, "print the same structured result as the MCP tool")
	return cmd
}

// queryKnowledgeBaseSettings assembles common read options at the CLI boundary.
func queryKnowledgeBaseSettings(cmd *cobra.Command) *kwb.Settings {
	settings := kwb.NewSettings()
	settings.RootPath = viper.GetString("root")
	settings.IndexPath = viper.GetString("index")
	settings.RequireRootMatch = cmdutil.ExplicitFlag(cmd, "root")
	if settings.RequireRootMatch && !cmdutil.ExplicitFlag(cmd, "index") {
		settings.IndexPath = filepath.Join(settings.RootPath, kwb.NewSettings().IndexPath)
	}
	settings.SearchTimeout = viper.GetDuration("search-timeout")
	return settings
}

func runSearchCommand(f *cmdutil.Factory, settings *kwb.SearchSettings) (retErr error) {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, service.Close()) }()
	result, err := service.Search(f.Context(), settings.Search)
	if err != nil {
		return err
	}
	return writeKnowledgeBaseResult(f, result, settings.JSON)
}

func runReadCommand(f *cmdutil.Factory, settings *kwb.ReadSettings) (retErr error) {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, service.Close()) }()
	result, err := service.GetFile(f.Context(), settings.Path, settings.StartLine, settings.EndLine)
	if err != nil {
		return err
	}
	return writeKnowledgeBaseResult(f, result, settings.JSON)
}

func runStatsCommand(f *cmdutil.Factory, settings *kwb.StatsSettings) (retErr error) {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, service.Close()) }()
	result, err := service.GetStats(f.Context())
	if err != nil {
		return err
	}
	return writeKnowledgeBaseResult(f, result, settings.JSON)
}

func writeKnowledgeBaseResult(f *cmdutil.Factory, result any, asJSON bool) error {
	if asJSON {
		return cmdutil.JSON(f.Output(), result)
	}
	return printKnowledgeBaseResult(f, result)
}

func printKnowledgeBaseResult(f *cmdutil.Factory, result any) error {
	switch value := result.(type) {
	case *kwb.SearchResponse:
		for _, hit := range value.Results {
			if _, err := fmt.Fprintf(
				f.Output(),
				"%s:%d-%d %s\n%s\n\n",
				hit.Path,
				hit.StartLine,
				hit.EndLine,
				hit.Title,
				hit.Snippet,
			); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(f.Output(), "%d matches\n", value.Total); err != nil {
			return err
		}
		if value.NextOffset != nil {
			_, err := fmt.Fprintf(f.Output(), "Next offset: %d\n", *value.NextOffset)
			return err
		}
	case *kwb.FileContent:
		if _, err := fmt.Fprint(f.Output(), value.Content); err != nil {
			return err
		}
		if value.NextStartLine != nil {
			_, err := fmt.Fprintf(f.ErrorOutput(), "Continue with --start-line=%d\n", *value.NextStartLine)
			return err
		}
	case *kwb.Stats:
		_, err := fmt.Fprintf(
			f.Output(),
			"Root: %s\nIndex: %s\nGeneration: %s\nFiles: %d\nChunks: %d\n",
			value.RootPath,
			value.IndexPath,
			value.Generation,
			value.DocumentCount,
			value.ChunkCount,
		)
		return err
	}
	return nil
}
