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
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update bundled AI agent configuration and templates",
		Long: `Replace bundled configuration and templates with the versions shipped in this binary.
Use --base with a previously exported bundle to merge project customizations with this binary's assets.
Conflicts leave project files unchanged and save candidates under .build.
Local overrides and additional files are preserved.
Run 'go42x agentenv generate' afterward to refresh generated agent files.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings := &agentenv.Settings{
				BaseDir: viper.GetString(flagBase),
				JSON:    viper.GetBool(flagJSON),
			}
			if cmdutil.ExplicitFlag(cmd, flagBase) && len(settings.BaseDir) == 0 {
				return cmdutil.UsageError(fmt.Errorf("base directory is required when --base is set"))
			}

			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runUpdateCommand(f, settings)
		},
	}
	cmd.Flags().String(
		flagBase, "",
		"previously exported original bundle to use as the merge base")
	return cmd
}

func runUpdateCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(settings, agentenv.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportUpdate(f.Output(), settings, nil, fmt.Errorf("initialize agentenv service: %w", err))
	}

	result, err := service.Update(f.Context())
	return cli.ReportUpdate(f.Output(), settings, result, err)
}
