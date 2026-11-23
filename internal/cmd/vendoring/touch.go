package vendoring

import (
	"log/slog"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/generator"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/vendoring"
	"github.com/go42-dev/go42x/internal/vendoring/adapters/cli"
)

func newTouchCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := vendoring.NewSettings()
	return &cobra.Command{
		Use:   "touch [directory]",
		Short: "Create an empty vendoring manifest",
		Long: `Create .vendor.yaml in the supplied directory, leaving an existing manifest unchanged.
Relative directories resolve from --root. With no directory, use --root itself.`,
		Example: "  go42x vendor touch\n  go42x vendor touch api/proto/third_party",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			settings := &vendoring.Settings{
				RootPath:     viper.GetString(flagRoot),
				ManifestPath: viper.GetString(flagManifest),
				LockPath:     viper.GetString(flagLock),
				TmpDir:       defaults.TmpDir,
				JournalDir:   defaults.JournalDir,
				Timeout:      defaults.Timeout,
				JSON:         viper.GetBool(flagJSON),
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			directory := "."
			if len(args) == 1 {
				directory = args[0]
			}
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(settings.RootPath, directory)
			}
			return runTouchCommand(f, settings, directory)
		},
	}
}

func runTouchCommand(f *cmdutil.Factory, settings *vendoring.Settings, directory string) error {
	data, err := generator.ProjectContext(f.Context(), slog.New(slog.DiscardHandler), settings.RootPath)
	if err != nil {
		return cli.Report(f.Output(), settings, vendoring.OperationTouch, nil, err)
	}
	result, err := vendoring.Touch(f.Context(), directory, data)
	return cli.Report(f.Output(), settings, vendoring.OperationTouch, result, err)
}
