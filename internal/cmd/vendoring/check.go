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

func newCheckCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := vendoring.NewSettings()
	return &cobra.Command{
		Use:   "check",
		Short: "Check vendored files against the lockfile",
		Long: `Verify that the manifest matches the lockfile and that vendored files match
their recorded checksums. Runs offline without modifying files.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &vendoring.Settings{
				RootPath:     viper.GetString("root"),
				ManifestPath: viper.GetString("manifest"),
				LockPath:     viper.GetString("lock"),
				TempDir:      defaults.TempDir,
				Timeout:      defaults.Timeout,
				JSON:         viper.GetBool("json"),
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runCheckCommand(f, settings)
		},
	}
}

func runCheckCommand(f *cmdutil.Factory, settings *vendoring.Settings) error {
	if err := settings.LoadProject(); err != nil {
		return cli.Report(f.Output(), settings, "check", nil, err)
	}
	if err := settings.Validate(); err != nil {
		return cli.Report(f.Output(), settings, "check", nil, err)
	}
	if err := settings.Resolve(); err != nil {
		return cli.Report(f.Output(), settings, "check", nil, err)
	}
	service, err := vendoring.NewService(
		settings,
		vendoring.WithLogger(slog.Default()),
		vendoring.WithVersion(buildinfo.GetVersion()),
	)
	if err != nil {
		return cli.Report(f.Output(), settings, "check", nil, err)
	}
	result, err := service.Check(f.Context())
	return cli.Report(f.Output(), settings, "check", result, err)
}
