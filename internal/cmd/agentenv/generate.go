package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func newGenerateCommand(f *cmdutil.Factory, settings *agentenv.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate ai agent configuration",
		Long:  `Generate ai agent configuration`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerateCommand(f, settings)
		},
	}

	cmd.Flags().BoolVar(
		&settings.GenerateClean, "clean", false,
		"delete existing configuration files before generating new ones",
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
