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
		Long: `Merge project customizations with the configuration and templates shipped in this binary.
The merge base is the previous bundle, which Git fetches from the revision recorded in
.go42x/.bundle.lock.yaml; the record then names this binary's bundle.
Bundled resources, skills, and upstream files are replaced, and the local changes this replaces are listed.
Conflicts leave project files unchanged and save candidates under .build.
Local overrides and additional files are preserved.
Without a usable record, run 'go42x agentenv init --force' to reinstall the bundle instead.
Run 'go42x agentenv generate' afterward to refresh generated agent files.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings := &agentenv.Settings{JSON: viper.GetBool(flagJSON)}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			return runUpdateCommand(f, settings)
		},
	}
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
