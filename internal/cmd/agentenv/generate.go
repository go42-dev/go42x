package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv"
	"github.com/go42-dev/go42x/internal/agentenv/adapters/cli"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/config"
)

func newGenerateCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate ai agent configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &agentenv.Settings{
				Clean: viper.GetBool("clean"),
				JSON:  viper.GetBool("json"),
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
			return runGenerateCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		"clean", false,
		"regenerate owned instruction files after all outputs are prepared")
	cmd.Flags().String(
		"providers", "",
		"exact comma-separated provider list (empty selects none); overrides GO42X_PROVIDERS")

	return cmd
}

func runGenerateCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(
		settings,
		agentenv.WithLogger(slog.Default()),
	)
	if err != nil {
		return cli.ReportGenerate(
			f.Output(),
			settings,
			nil,
			fmt.Errorf("failed to initialize agentenv service: %w", err),
		)
	}
	result, err := service.Generate(f.Context())
	return cli.ReportGenerate(f.Output(), settings, result, err)
}
