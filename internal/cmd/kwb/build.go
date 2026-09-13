package kwb

import (
	"fmt"
	"log/slog"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func runBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
	service, err := kwb.NewService(
		settings,
		kwb.WithLogger(slog.Default().With("component", "kwb-service")),
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer service.Close() // nolint:errcheck

	if _, err := service.BuildIndex(f.Context(), settings.RootPath); err != nil {
		return fmt.Errorf("failed to build index: %w", err)
	}

	return nil
}
