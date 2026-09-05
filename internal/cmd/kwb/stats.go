package kwb

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newStatsCommand(f *cmdutil.Factory, settings *kwb.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show index statistics",
		Long:  `Display statistics about the knowledge base index`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatsCommand(f, settings)
		},
	}
	return cmd
}

func runStatsCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
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

	stats, err := service.GetStats(f.Context())
	if err != nil {
		return fmt.Errorf("failed to get stats: %w", err)
	}

	slog.Default().Info("Index Statistics", slog.Any("stats", stats))

	return nil
}
