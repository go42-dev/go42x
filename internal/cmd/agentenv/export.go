package agentenv

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv"
	"github.com/go42-dev/go42x/internal/agentenv/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newExportCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export this binary's bundled agent configuration and templates",
		Long: `Export bundled sources into a new directory without reading project configuration.
The destination must not already exist. Schema references match this binary's revision.`,
		Example: "  go42x agentenv export --output .build/go42x-bundle",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &agentenv.ExportSettings{
				OutputDir: viper.GetString(flagOutput),
				JSON:      viper.GetBool(flagJSON),
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runExportCommand(f, settings)
		},
	}

	cmd.Flags().String(
		flagOutput, "",
		"new directory for the exported bundle (required)")

	return cmd
}

func runExportCommand(f *cmdutil.Factory, settings *agentenv.ExportSettings) error {
	service, err := agentenv.NewAgentEnvService(&agentenv.Settings{}, agentenv.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportExport(f.Output(), settings, nil, err)
	}

	result, err := service.Export(f.Context(), settings.OutputDir)
	return cli.ReportExport(f.Output(), settings, result, err)
}
