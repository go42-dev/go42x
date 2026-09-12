package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/version"
	"github.com/go42-dev/go42x/pkg/kwb"
	kwbmcp "github.com/go42-dev/go42x/pkg/kwb/adapters/mcp"
	"github.com/go42-dev/go42x/pkg/mcpserver"
)

type mcpSettings struct {
	IndexPath     string
	Toolsets      []string
	SearchTimeout time.Duration
}

func NewMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "mcp",
		Short:       "Start the go42x MCP server over stdio",
		Long:        "Start the go42x MCP server over stdio. Select feature groups with --toolsets. ",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"mcp-stdio": "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &mcpSettings{
				IndexPath:     viper.GetString("index"),
				Toolsets:      viper.GetStringSlice("toolsets"),
				SearchTimeout: viper.GetDuration("search-timeout"),
			}
			return runMCPCommand(cmd, settings)
		},
	}

	cmd.Flags().StringSlice("toolsets", []string{"kwb"}, "comma-separated tool groups to enable (available: kwb)")
	cmd.Flags().String("index", kwb.NewSettings().IndexPath, "knowledge-base index path")
	cmd.Flags().Duration("search-timeout", kwb.NewSettings().SearchTimeout, "maximum duration of knowledge-base reads")

	return cmd
}

func runMCPCommand(cmd *cobra.Command, settings *mcpSettings) (retErr error) {
	kwbSettings := kwb.NewSettings()
	kwbSettings.IndexPath = settings.IndexPath
	kwbSettings.SearchTimeout = settings.SearchTimeout

	service, err := kwb.NewService(kwbSettings,
		kwb.WithLogger(slog.Default().With("component", "kwb-service")))
	if err != nil {
		return fmt.Errorf("creating knowledge-base service: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, service.Close())
	}()

	runtime, err := mcpserver.New(
		[]mcpserver.Toolset{
			kwbmcp.New(service),
		},
		mcpserver.WithLogger(slog.Default().With("component", "mcp-server")),
		mcpserver.WithVersion(version.GetVersion()),
		mcpserver.WithToolsets(settings.Toolsets...),
	)
	if err != nil {
		return err
	}

	return runtime.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
}
