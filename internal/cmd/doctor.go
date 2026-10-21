package cmd

import (
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/doctor"
	"github.com/go42-dev/go42x/internal/doctor/adapters/cli"
)

func NewDoctorCommand(f *cmdutil.Factory) *cobra.Command {
	defaults := doctor.NewSettings()
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose project configuration and setup",
		Long: `Check agent configuration, generated files, MCP server setup, and index freshness.
Use --probe-mcp to test MCP server connections.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &doctor.Settings{
				RootPath:  viper.GetString(flagRoot),
				IndexPath: viper.GetString(flagIndex),
				ProbeMCP:  viper.GetBool(flagProbeMCP),
				Timeout:   viper.GetDuration(flagTimeout),
				JSON:      viper.GetBool(flagJSON),
			}
			if cmdutil.ExplicitFlag(cmd, flagProviders) {
				var err error
				settings.Providers, err = provider.ParseProviders(
					viper.GetString(flagProviders),
					config.ProviderNames(),
				)
				if err != nil {
					return cmdutil.UsageError(err)
				}
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runDoctorCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		flagJSON, false,
		"print structured diagnostics")
	cmd.Flags().String(
		flagRoot, defaults.RootPath,
		"project root to inspect")
	cmd.Flags().Bool(
		flagProbeMCP, false,
		"start/connect to enabled servers, initialize, and list tools (no tool calls)")
	cmd.Flags().Duration(
		flagTimeout, defaults.Timeout,
		"maximum duration of each MCP probe")
	cmd.Flags().String(
		flagIndex, "",
		"knowledge-base index path (default .go42x/kwb/index)")
	cmd.Flags().String(
		flagProviders, "",
		"exact comma-separated provider list (empty selects none); overrides GO42X_PROVIDERS")

	return cmd
}

func runDoctorCommand(f *cmdutil.Factory, settings *doctor.Settings) error {
	service, err := doctor.NewService(settings, doctor.WithLogger(slog.Default()))
	if err != nil {
		return cli.Report(f.Output(), settings, nil, err)
	}

	report, err := service.Run(f.Context())
	return cli.Report(f.Output(), settings, report, err)
}
