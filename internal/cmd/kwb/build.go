package kwb

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func newBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build or rebuild the knowledge base index",
		Long:  `Build or rebuild the knowledge base index`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildCommand(f, settings)
		},
	}

	cmd.Flags().StringVar(&settings.RootPath, "root", ".", "root directory to index")
	cmd.Flags().IntVar(&settings.MaxFileSize, "max-file-size", 5*1024*1024, "maximum file size to index in bytes")
	cmd.Flags().IntVar(&settings.BatchSize, "batch-size", 100, "number of documents to index in a batch")
	cmd.Flags().StringVar(&settings.IndexType, "index-type", "scorch", "index type: scorch or upsidedown")
	cmd.Flags().StringSliceVar(&settings.ExcludeDirs, "exclude-dir", nil, "additional directories to exclude")
	cmd.Flags().StringSliceVar(&settings.ExtraExtensions, "include-ext", nil, "additional file extensions to index")

	return cmd
}

func runBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
	service, err := kwb.NewService(
		settings,
		kwb.WithLogger(slog.Default().With("component", "kwb-service")),
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer service.Close() // nolint:errcheck

	if err := service.BuildIndex(f.Context(), settings.RootPath); err != nil {
		return fmt.Errorf("failed to build index: %w", err)
	}

	return nil
}
