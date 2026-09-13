package agentenv

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
)

func NewAgentEnvCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agentenv",
		Short: "AI environment configuration",
		Long:  `AI environment configuration`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newInitCommand(f))
	cmd.AddCommand(newGenerateCommand(f))

	return cmd
}
