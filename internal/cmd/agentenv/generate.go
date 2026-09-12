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
				OutputPath:    viper.GetString("output"),
				GenerateClean: viper.GetBool("clean"),
			}
			return runGenerateCommand(f, settings)
		},
	}

	cmd.Flags().Bool(
		"clean", false,
		"remove generated instruction files before regenerating them",
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
