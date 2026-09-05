package kwb

import (
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/kwb"
)

func NewKnowledgeBaseCommand(f *cmdutil.Factory) *cobra.Command {
	settings := new(kwb.Settings)

	cmd := &cobra.Command{
		Use:   "kwb",
		Short: "Knowledge base management",
		Long:  `Manage full-text search knowledge base for the codebase`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&settings.IndexPath, "index", ".agentenv/kwb/index", "path to the index")

	cmd.AddCommand(newBuildCommand(f, settings))
	cmd.AddCommand(newSearchCommand(f, settings))
	cmd.AddCommand(newServeCommand(f, settings))
	cmd.AddCommand(newStatsCommand(f, settings))

	return cmd
}
