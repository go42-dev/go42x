package agentenv

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func NewAgentEnvCommand(f *cmdutil.Factory) *cobra.Command {
	settings := new(agentenv.Settings)

	cmd := &cobra.Command{
		Use:   "agentenv",
		Short: "AI environment configuration",
		Long:  `AI environment configuration`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.Flags().StringVarP(&settings.OutputPath, "output", "o", ".", "path to output directory")

	cmd.AddCommand(newInitCommand(f, settings))
	cmd.AddCommand(newGenerateCommand(f, settings))
	cmd.AddCommand(newAnalyseCommand(f, settings))

	return cmd
}
