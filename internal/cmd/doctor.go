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
				RootPath:  viper.GetString("root"),
				IndexPath: viper.GetString("index"),
				ProbeMCP:  viper.GetBool("probe-mcp"),
				Timeout:   viper.GetDuration("timeout"),
				JSON:      viper.GetBool("json"),
			}
			if cmdutil.ExplicitFlag(cmd, "providers") {
				var err error
				settings.Providers, err = provider.ParseProviders(viper.GetString("providers"), config.ProviderNames())
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

	cmd.Flags().
		Bool("json", false, "print structured diagnostics")
	cmd.Flags().
		String("root", defaults.RootPath, "project root to inspect")
	cmd.Flags().
		Bool("probe-mcp", false, "start/connect to enabled servers, initialize, and list tools (no tool calls)")
	cmd.Flags().
		Duration("timeout", defaults.Timeout, "maximum duration of each MCP probe")
	cmd.Flags().
		String("index", "", "knowledge-base index path (default .go42x/kwb/index)")
	cmd.Flags().
		String("providers", "", "exact comma-separated provider list (empty selects none); overrides GO42X_PROVIDERS")

	return cmd
}

func runDoctorCommand(f *cmdutil.Factory, settings *doctor.Settings) error {
	service, err := doctor.NewService(settings, doctor.WithLogger(slog.Default().With("component", "doctor-service")))
	if err != nil {
		return cli.Report(f.Output(), settings, nil, err)
	}
	report, err := service.Run(f.Context())
	return cli.Report(f.Output(), settings, report, err)
}
