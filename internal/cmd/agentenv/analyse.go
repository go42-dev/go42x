package agentenv

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmdutil"
	"github.com/go42-dev/go42x/pkg/agentenv"
)

func newAnalyseCommand(f *cmdutil.Factory, settings *agentenv.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyse",
		Short: "Analyse project and generate memory",
		Long:  `Analyse project and generate memory`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyseCommand(f, settings)
		},
	}

	cmd.Flags().StringVar(
		&settings.AnalysisProvider, "provider", "",
		"provider to use (claude,gemini)",
	)
	cmd.Flags().StringVar(
		&settings.AnalysisModel, "model", "",
		"model to use, must be compatible with the provider",
	)
	cmd.Flags().DurationVar(
		&settings.AnalysisTimeout, "timeout", 5*time.Minute,
		"timeout for analysis in seconds",
	)

	return cmd
}

func runAnalyseCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
	service, err := agentenv.NewAgentEnvService(
		settings,
		agentenv.WithLogger(slog.Default()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize agentenv service: %w", err)
	}
	return service.Analyse(f.Context())
}
