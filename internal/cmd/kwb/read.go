package kwb

import (
	"errors"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/adapters/cli"
)

func newReadCommand(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read PATH",
		Short: "Read lines from current project files",
		Long:  "Read current file contents from disk. Relative paths resolve from the project root.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := &kwb.ReadSettings{
				KnowledgeBase: queryKnowledgeBaseSettings(cmd),
				Path:          args[0],
				StartLine:     viper.GetInt("start-line"),
				EndLine:       viper.GetInt("end-line"),
				JSON:          viper.GetBool("json"),
			}
			if !cmdutil.ExplicitFlag(cmd, "end-line") {
				settings.EndLine = settings.StartLine + 199
			}
			if err := settings.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}
			return runReadCommand(f, settings)
		},
	}

	cmd.Flags().Bool("json", false, "print structured file content")
	cmd.Flags().Int("start-line", 1, "first line, one-based")
	cmd.Flags().Int("end-line", 0, "last line, inclusive (default start-line + 199)")

	return cmd
}

func runReadCommand(f *cmdutil.Factory, settings *kwb.ReadSettings) error {
	service, err := kwb.NewService(settings.KnowledgeBase, kwb.WithLogger(slog.Default()))
	if err != nil {
		return cli.ReportRead(f.Output(), f.ErrorOutput(), settings, nil, err)
	}
	result, err := service.GetFile(f.Context(), settings.Path, settings.StartLine, settings.EndLine)
	err = errors.Join(err, service.Close())
	return cli.ReportRead(f.Output(), f.ErrorOutput(), settings, result, err)
}
