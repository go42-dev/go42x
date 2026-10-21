package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/agentenv"
	"github.com/go42-dev/go42x/internal/agentenv/adapters/cli"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

func newInitCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize AI agent configuration and templates",
		Long: `Install missing configuration and templates in the current project.
Existing configuration and templates are preserved.
Run 'go42x agentenv generate' to generate agent instructions and settings.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &agentenv.Settings{JSON: viper.GetBool(flagJSON)}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

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
		return cli.ReportInit(f.Output(), settings, nil, fmt.Errorf("failed to initialize agentenv service: %w", err))
	}

	result, err := service.Init(f.Context())
	return cli.ReportInit(f.Output(), settings, result, err)
}
