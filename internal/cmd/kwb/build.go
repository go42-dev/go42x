package kwb

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/adapters/cli"
)

func newBuildCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := kwb.NewSettings()
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build or update the project knowledge-base index",
		Long: `Index project documentation and source files for knowledge-base search.
Only changed files are reindexed. Use --rebuild to rebuild the entire index.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &kwb.Settings{
				RootPath:            viper.GetString("root"),
				IndexPath:           viper.GetString("index"),
				MaxFileSize:         viper.GetInt("max-file-size"),
				BatchSize:           viper.GetInt("batch-size"),
				IndexType:           viper.GetString("index-type"),
				ExcludeDirs:         viper.GetStringSlice("exclude-dir"),
				ExcludeFiles:        viper.GetStringSlice("exclude-file"),
				ExtraExtensions:     viper.GetStringSlice("include-ext"),
				Rebuild:             viper.GetBool("rebuild"),
				JSON:                viper.GetBool("json"),
				SearchTimeout:       defaults.SearchTimeout,
				SearchLimit:         defaults.SearchLimit,
				DefaultContentBytes: defaults.DefaultContentBytes,
			}

			if cmdutil.ExplicitFlag(cmd, "root") && !cmdutil.ExplicitFlag(cmd, "index") {
				settings.IndexPath = filepath.Join(settings.RootPath, defaults.IndexPath)
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runBuildCommand(f, settings)
		},
	}

	cmd.Flags().
		Bool("rebuild", false, "rebuild the entire index")
	cmd.Flags().
		Int("max-file-size", defaults.MaxFileSize, "maximum file size to index in bytes")
	cmd.Flags().
		Int("batch-size", defaults.BatchSize, "maximum number of chunks to update in a batch")
	cmd.Flags().
		String("index-type", defaults.IndexType, "index type: scorch or upsidedown")
	cmd.Flags().
		StringSlice("exclude-dir", defaults.ExcludeDirs, "additional directories to exclude")
	cmd.Flags().
		StringSlice("exclude-file", nil, "additional root-relative gitignore patterns (also reads .go42x/kwb.ignore)")
	cmd.Flags().
		StringSlice("include-ext", defaults.ExtraExtensions, "additional file extensions or filenames to index")

	cmd.Flags().Bool("json", false, "print structured build results")

	return cmd
}

func runBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
	service, err := kwb.NewService(settings, kwb.WithLogger(slog.Default().With("component", "kwb-service")))
	if err != nil {
		return cli.ReportBuild(f.Output(), settings, nil, fmt.Errorf("failed to create service: %w", err))
	}
	result, err := service.BuildIndex(f.Context(), settings.RootPath)
	err = errors.Join(err, service.Close())
	return cli.ReportBuild(f.Output(), settings, &result, err)
}
