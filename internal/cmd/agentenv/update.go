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

func newUpdateCommand(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update bundled AI agent configuration and templates",
		Long: `Replace bundled configuration and templates with the versions shipped in this binary.
Local overrides and additional files are preserved.
Run 'go42x agentenv generate' afterward to refresh generated agent files.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &agentenv.Settings{JSON: viper.GetBool("json")}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runUpdateCommand(f, settings)
		},
	}
}

func runUpdateCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(settings, agentenv.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportUpdate(f.Output(), settings, nil, fmt.Errorf("initialize agentenv service: %w", err))
	}
	result, err := service.Update(f.Context())
	return cli.ReportUpdate(f.Output(), settings, result, err)
}
