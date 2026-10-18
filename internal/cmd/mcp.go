package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	kwbmcp "github.com/go42-dev/go42x/internal/kwb/adapters/mcp"
	"github.com/go42-dev/go42x/internal/mcpserver"
)

func NewMCPCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the go42x MCP server over stdio",
		Long: `Expose project search, documentation, and context tools to MCP clients over stdio.
Run 'go42x kwb build' to create or refresh the search index.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &mcpserver.Settings{
				IndexPath:     viper.GetString("index"),
				RootPath:      viper.GetString("root"),
				ExplicitRoot:  cmdutil.ExplicitFlag(cmd, "root"),
				SearchTimeout: viper.GetDuration("search-timeout"),
				ContextDocs:   viper.GetStringSlice("context-doc"),
			}
			if settings.ExplicitRoot && !cmdutil.ExplicitFlag(cmd, "index") {
				settings.IndexPath = filepath.Join(settings.RootPath, kwb.NewSettings().IndexPath)
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runMCPCommand(f, settings)
		},
	}

	cmd.Flags().
		String("root", ".", "project root (default indexed project root, or current directory without an index)")
	cmd.Flags().
		String("index", kwb.NewSettings().IndexPath, "knowledge-base index path")
	cmd.Flags().
		Duration("search-timeout", kwb.NewSettings().SearchTimeout, "maximum duration of knowledge-base reads")
	cmd.Flags().
		StringSlice("context-doc", nil, "authored document IDs to include as project guidance (at most 8)")

	return cmd
}

func runMCPCommand(f *cmdutil.Factory, settings *mcpserver.Settings) (retErr error) {
	kwbSettings := settings.KnowledgeBaseSettings()

	service, err := kwb.NewService(kwbSettings,
		kwb.WithLogger(slog.Default().With("component", "kwb-service")))
	if err != nil {
		return fmt.Errorf("creating knowledge-base service: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, service.Close())
	}()

	runtime, err := mcpserver.New(
		mcpserver.WithLogger(slog.Default().With("component", "mcp-server")),
		mcpserver.WithVersion(buildinfo.GetVersion()),
	)
	if err != nil {
		return err
	}

	if err := runtime.AddToolset(kwbmcp.New(service)); err != nil {
		return err
	}

	if _, err := service.ProjectRoot(); err != nil {
		return err
	}

	return runtime.Serve(f.Context(), f.Input(), f.Output())
}
