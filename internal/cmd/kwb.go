package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func NewKnowledgeBaseCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := kwb.NewSettings()

	cmd := &cobra.Command{
		Use:   "kwb",
		Short: "Update the knowledge base index",
		Long:  "Index changed documentation and source files. Use --rebuild to rebuild the full index.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &kwb.Settings{
				RootPath:        viper.GetString("root"),
				IndexPath:       viper.GetString("index"),
				MaxFileSize:     viper.GetInt("max-file-size"),
				BatchSize:       viper.GetInt("batch-size"),
				IndexType:       viper.GetString("index-type"),
				ExcludeDirs:     viper.GetStringSlice("exclude-dir"),
				ExtraExtensions: viper.GetStringSlice("include-ext"),
				SearchTimeout:   defaults.SearchTimeout,
				SearchLimit:     defaults.SearchLimit,
				Rebuild:         viper.GetBool("rebuild"),
			}
			return runKnowledgeBaseCommand(f, settings)
		},
	}

	cmd.Flags().String("index", defaults.IndexPath, "path to the index")
	cmd.Flags().String("root", defaults.RootPath, "root directory to index")
	cmd.Flags().Bool("rebuild", false, "rebuild the full index before replacing the current generation")
	cmd.Flags().Int("max-file-size", defaults.MaxFileSize, "maximum file size to index in bytes")
	cmd.Flags().Int("batch-size", defaults.BatchSize, "maximum number of chunks to update in a batch")
	cmd.Flags().String("index-type", defaults.IndexType, "index type: scorch or upsidedown")
	cmd.Flags().StringSlice("exclude-dir", defaults.ExcludeDirs, "additional directories to exclude")
	cmd.Flags().StringSlice("include-ext", defaults.ExtraExtensions, "additional file extensions to index")

	return cmd
}

func runKnowledgeBaseCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
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
