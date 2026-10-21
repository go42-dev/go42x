package agentenv

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
)

const (
	flagBase      = "base"
	flagClean     = "clean"
	flagJSON      = "json"
	flagOutput    = "output"
	flagProviders = "providers"
)

func NewAgentEnvCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agentenv",
		Short: "Manage AI agent configuration and templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().Bool(
		flagJSON, false,
		"print structured results")

	cmd.AddCommand(newInitCommand(f))
	cmd.AddCommand(newUpdateCommand(f))
	cmd.AddCommand(newExportCommand(f))
	cmd.AddCommand(newGenerateCommand(f))

	return cmd
}
