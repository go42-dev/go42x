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

func newUpdateCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := vendoring.NewSettings()
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update vendored files and the lockfile",
		Long: `Update vendored files and the lockfile using the sources declared in the manifest.
Branches and npm tags are resolved again, so their versions may change.`,
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
			return runUpdateCommand(f, settings)
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

func runUpdateCommand(f *cmdutil.Factory, settings *vendoring.Settings) error {
	if err := settings.LoadProject(); err != nil {
		return cli.Report(f.Output(), settings, "update", nil, err)
	}
	if err := settings.Validate(); err != nil {
		return cli.Report(f.Output(), settings, "update", nil, err)
	}
	if err := settings.Resolve(); err != nil {
		return cli.Report(f.Output(), settings, "update", nil, err)
	}
	service, err := vendoring.NewService(
		settings,
		vendoring.WithLogger(slog.Default()),
		vendoring.WithVersion(buildinfo.GetVersion()),
	)
	if err != nil {
		return cli.Report(f.Output(), settings, "update", nil, err)
	}
	result, err := service.Update(f.Context())
	return cli.Report(f.Output(), settings, "update", result, err)
}
