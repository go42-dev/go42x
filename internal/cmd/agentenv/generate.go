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
		Long:  `Generate ai agent configuration`,
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := &agentenv.Settings{
				Clean: viper.GetBool("clean"),
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
