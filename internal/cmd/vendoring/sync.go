package vendoring

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/buildinfo"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/vendoring"
	"github.com/go42-dev/go42x/internal/vendoring/adapters/cli"
)

func newSyncCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := vendoring.NewSettings()
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Restore vendored files from the lockfile",
		Long: `Restore the source versions recorded in the lockfile without updating it.
The manifest must match the lockfile. Sources may be downloaded.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &vendoring.Settings{
				RootPath:     viper.GetString("root"),
				ManifestPath: viper.GetString("manifest"),
				LockPath:     viper.GetString("lock"),
				TempDir:      defaults.TempDir,
				Timeout:      defaults.Timeout,
				DryRun:       viper.GetBool("dry-run"),
				Force:        viper.GetBool("force"),
				JSON:         viper.GetBool("json"),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runSyncCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		"dry-run", false,
		"preview changes without modifying destinations, lock or backups")
	cmd.Flags().Bool(
		"force", false,
		"back up and replace locally modified or conflicting targets")

	return cmd
}

func runSyncCommand(f *cmdutil.Factory, settings *vendoring.Settings) error {
	if err := settings.LoadProject(); err != nil {
		return cli.Report(f.Output(), settings, "sync", nil, err)
	}
	if err := settings.Validate(); err != nil {
		return cli.Report(f.Output(), settings, "sync", nil, err)
	}
	if err := settings.Resolve(); err != nil {
		return cli.Report(f.Output(), settings, "sync", nil, err)
	}
	service, err := vendoring.NewService(
		settings,
		vendoring.WithLogger(slog.Default()),
		vendoring.WithVersion(buildinfo.GetVersion()),
	)
	if err != nil {
		return cli.Report(f.Output(), settings, "sync", nil, err)
	}
	result, err := service.Sync(f.Context())
	return cli.Report(f.Output(), settings, "sync", result, err)
}
