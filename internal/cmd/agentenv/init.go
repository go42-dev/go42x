package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func newInitCommand(f *cmdutil.Factory, settings *agentenv.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise ai agent configuration",
		Long:  `Initialise ai agent configuration`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInitCommand(f, settings)
		},
	}
	return cmd
}

func runInitCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(
		settings,
		agentenv.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize agentenv service: %w", err)
	}
	return service.Init(f.Context())
}
