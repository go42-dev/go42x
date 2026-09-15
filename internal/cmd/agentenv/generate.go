package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func newGenerateCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate ai agent configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			providers, err := cmdutil.ProviderSelection(cmd)
			if err != nil {
				return cmdutil.UsageError(err)
			}
			settings := &agentenv.Settings{
				Clean:     viper.GetBool("clean"),
				Providers: providers,
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runGenerateCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		"clean", false,
		"regenerate owned instruction files after all outputs are prepared",
	)
	cmd.Flags().
		String("providers", "", "exact comma-separated provider list (empty selects none); overrides GO42X_PROVIDERS")

	return cmd
}

func runGenerateCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(
		settings,
		agentenv.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize agentenv service: %w", err)
	}
	return service.Generate(f.Context())
}
