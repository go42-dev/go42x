package agentenv

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func newUpdateCommand(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Back up and replace agentenv sources, then regenerate outputs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			providers, err := cmdutil.ProviderSelection(cmd)
			if err != nil {
				return cmdutil.UsageError(err)
			}
			service, err := agentenv.NewAgentEnvService(
				&agentenv.Settings{Providers: providers}, agentenv.WithLogger(slog.Default()),
			)
			if err != nil {
				return fmt.Errorf("initialize agentenv service: %w", err)
			}
			result, err := service.Update(f.Context())
			if result != nil {
				_, printErr := fmt.Fprintf(f.Output(), "Backup: %s\n", result.BackupDir)
				if printErr != nil && err == nil {
					return printErr
				}
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(f.Output(), "Updated %d source files and regenerated %d output files.\n",
				result.Sources, result.Outputs)
			return err
		},
	}
}
