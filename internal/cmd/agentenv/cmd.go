package agentenv

import (
	"github.com/spf13/cobra"

	journalcmd "github.com/go42-dev/go42x/internal/cmd/agentenv/journal"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

const (
	flagClean     = "clean"
	flagForce     = "force"
	flagJSON      = "json"
	flagNoLocal   = "no-local"
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
	cmd.AddCommand(newGenerateCommand(f))
	cmd.AddCommand(journalcmd.NewJournalCommand(f))

	return cmd
}
