package kwb

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newSearchCommand(f *cmdutil.Factory, settings *kwb.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the knowledge base",
		Long:  `Search the knowledge base using full-text search`,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			return runSearchCommand(f, settings, query)
		},
	}

	cmd.Flags().IntVar(
		&settings.SearchLimit, "limit", 10, "maximum number of results")
	cmd.Flags().BoolVar(
			&settings.SearchShowScore, "show_score", false, "show relevance scores")
	cmd.Flags().DurationVar(
			&settings.SearchTimeout, "timeout", 5*time.Second, "search timeout duration")
	cmd.Flags().IntVar(
		&settings.SearchFuzziness, "fuzzy", 0, "fuzzy search distance (0=exact, 1-2=fuzzy)")
	cmd.Flags().StringVar(
			&settings.HighlightStyle, "highlight", "ansi", "highlight style: ansi or html")

	return cmd
}

func runSearchCommand(f *cmdutil.Factory, settings *kwb.Settings, query string) error {
	if !settings.IndexExists() {
		return fmt.Errorf("index not found at %s, run 'kwb build' first", settings.IndexPath)
	}

	service, err := kwb.NewService(
		settings,
		kwb.WithLogger(slog.Default().With("component", "kwb-service")),
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer service.Close() // nolint:errcheck

	results, err := service.Search(f.Context(), query, settings.SearchLimit)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		slog.Default().Info("No results found")
		return nil
	}

	slog.Default().Info("Search completed", slog.Int("results", len(results)))

	for i, result := range results {
		slog.Default().Info("Search result",
			slog.Int("index", i+1),
			slog.String("path", result.Path),
			slog.String("type", result.Type),
			slog.Float64("score", result.Score),
			slog.Bool("showScore", settings.SearchShowScore),
		)
		if result.Preview != "" {
			// Clean up the preview
			preview := strings.ReplaceAll(result.Preview, "\n", " ")
			preview = strings.TrimSpace(preview)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			slog.Default().Info("Result preview",
				slog.Int("index", i+1),
				slog.String("preview", preview),
			)
		}
	}

	return nil
}
