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
				RootPath:            viper.GetString(flagRoot),
				IndexPath:           viper.GetString(flagIndex),
				MaxFileSize:         viper.GetInt(flagMaxFileSize),
				BatchSize:           viper.GetInt(flagBatchSize),
				IndexType:           viper.GetString(flagIndexType),
				ExcludeDirs:         viper.GetStringSlice(flagExcludeDir),
				ExcludeFiles:        viper.GetStringSlice(flagExcludeFile),
				ExtraExtensions:     viper.GetStringSlice(flagIncludeExt),
				Rebuild:             viper.GetBool(flagRebuild),
				JSON:                viper.GetBool(flagJSON),
				SearchTimeout:       defaults.SearchTimeout,
				SearchLimit:         defaults.SearchLimit,
				DefaultContentBytes: defaults.DefaultContentBytes,
			}

			if cmdutil.ExplicitFlag(cmd, flagRoot) && !cmdutil.ExplicitFlag(cmd, flagIndex) {
				settings.IndexPath = filepath.Join(settings.RootPath, defaults.IndexPath)
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runBuildCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		flagRebuild, false,
		"rebuild the entire index")
	cmd.Flags().Int(
		flagMaxFileSize, defaults.MaxFileSize,
		"maximum file size to index in bytes")
	cmd.Flags().Int(
		flagBatchSize, defaults.BatchSize,
		"maximum number of chunks to update in a batch")
	cmd.Flags().String(
		flagIndexType, defaults.IndexType,
		"index type: scorch or upsidedown")
	cmd.Flags().StringSlice(
		flagExcludeDir, defaults.ExcludeDirs,
		"additional directories to exclude")
	cmd.Flags().StringSlice(
		flagExcludeFile, nil,
		"additional root-relative gitignore patterns (also reads .go42x/kwb.ignore)")
	cmd.Flags().StringSlice(
		flagIncludeExt, defaults.ExtraExtensions,
		"additional file extensions or filenames to index")
	cmd.Flags().Bool(
		flagJSON, false,
		"print structured build results")

	return cmd
}

func runBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
	service, err := kwb.NewService(settings, kwb.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportBuild(f.Output(), settings, nil, fmt.Errorf("failed to create service: %w", err))
	}

	result, err := service.BuildIndex(f.Context(), settings.RootPath)
	err = errors.Join(err, service.Close())
	return cli.ReportBuild(f.Output(), settings, &result, err)
}
