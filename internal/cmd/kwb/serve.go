package kwb

import (
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newServeCommand(f *cmdutil.Factory, settings *kwb.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server",
		Long:  `Start the knowledge base MCP server`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeCommand(f, settings)
		},
	}
	return cmd
}

func runServeCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
	ctx, cancel := signal.NotifyContext(f.Context(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

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

	server := kwb.NewMCPServer(service)

	if err := server.Serve(ctx); err != nil {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}
