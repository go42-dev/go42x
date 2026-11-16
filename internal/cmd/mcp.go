package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/journal"
	journalmcp "github.com/go42-dev/go42x/internal/agentenv/journal/adapters/mcp"
	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	kwbmcp "github.com/go42-dev/go42x/internal/kwb/adapters/mcp"
	"github.com/go42-dev/go42x/internal/mcpserver"
)

// refresherShutdownTimeout bounds the wait for a canceled index refresh to clean up.
const refresherShutdownTimeout = 10 * time.Second

func NewMCPCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := kwb.NewSettings()

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the go42x MCP server over stdio",
		Long: `Expose project search, documentation, context, and journal tools to MCP clients over stdio.
Run 'go42x kwb build' to create the search index. The server refreshes an existing index in the
background when it starts and after searches return stale results. It keeps the source selection
of the last build and never rewrites exclusions; run 'go42x kwb build' after changing them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &mcpserver.Settings{
				IndexPath:     viper.GetString(flagIndex),
				RootPath:      viper.GetString(flagRoot),
				ExplicitRoot:  cmdutil.ExplicitFlag(cmd, flagRoot),
				SearchTimeout: viper.GetDuration(flagSearchTimeout),
				ContextDocs:   viper.GetStringSlice(flagContextDoc),
				IndexRefresh:  viper.GetBool(flagIndexRefresh),
			}
			if settings.ExplicitRoot && !cmdutil.ExplicitFlag(cmd, flagIndex) {
				settings.IndexPath = filepath.Join(settings.RootPath, defaults.IndexPath)
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runMCPCommand(f, settings)
		},
	}

	cmd.Flags().String(
		flagRoot, ".",
		"project root (default indexed project root, or current directory without an index)")
	cmd.Flags().String(
		flagIndex, defaults.IndexPath,
		"knowledge-base index path")
	cmd.Flags().Duration(
		flagSearchTimeout, defaults.SearchTimeout,
		"maximum duration of knowledge-base reads")
	cmd.Flags().StringSlice(
		flagContextDoc, nil,
		"authored document IDs to include as project guidance (at most 8)")
	cmd.Flags().Bool(
		flagIndexRefresh, true,
		"refresh an existing index at startup and after searches return stale results")

	return cmd
}

func runMCPCommand(f *cmdutil.Factory, settings *mcpserver.Settings) (retErr error) {
	kwbSettings := settings.KnowledgeBaseSettings()

	service, err := kwb.NewService(kwbSettings,
		kwb.WithLogger(slog.Default()))
	if err != nil {
		return fmt.Errorf("creating knowledge-base service: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, service.Close())
	}()

	runtime, err := mcpserver.New(
		mcpserver.WithLogger(slog.Default()),
		mcpserver.WithVersion(buildinfo.GetVersion()),
	)
	if err != nil {
		return err
	}

	if err := runtime.AddToolset(kwbmcp.New(service)); err != nil {
		return err
	}

	root, err := service.ProjectRoot()
	if err != nil {
		return err
	}

	journalSettings := journal.NewSettings()
	journalSettings.RootPath = root
	journalService, err := journal.NewService(journalSettings, journal.WithLogger(slog.Default()))
	if err != nil {
		return fmt.Errorf("creating journal service: %w", err)
	}
	if err := runtime.AddToolset(journalmcp.New(journalService)); err != nil {
		return err
	}

	if settings.IndexRefresh {
		refresher := service.StartRefresher(f.Context())
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(f.Context()), refresherShutdownTimeout)
			defer cancel()
			retErr = errors.Join(retErr, refresher.Shutdown(ctx))
		}()
	}

	return runtime.Serve(f.Context(), f.Input(), f.Output())
}
