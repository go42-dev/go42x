package cmd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/go42-dev/go42x/internal/cmd/agentenv"
	"github.com/go42-dev/go42x/internal/cmd/kwb"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

const envPrefix = "GO42X"

func NewGo42Command(ctx context.Context, f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "go42x",
		Short: "Helper tool for go42 project",
		Long:  `Helper tool for go42 project`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Cobra has parsed and merged the selected command's local and
			// inherited flags by this point. Bind only that command's flags.
			if err := viper.BindPFlags(cmd.Flags()); err != nil {
				return err
			}

			options := f.Options()
			*options = cmdutil.Options{
				LogLevel: viper.GetString("log-level"),
			}
			if err := options.Validate(); err != nil {
				return cmdutil.UsageError(err)
			}

			f.SetIOStreams(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())

			output := f.Output()
			if cmd.Annotations["mcp-stdio"] == "true" || cmd.Annotations["result-output"] == "true" {
				output = f.ErrorOutput()
			}

			initLogging(options.LogLevel, output)

			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	cmd.SetContext(ctx)
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	cmd.PersistentFlags().String("log-level", "info", "Logging level (debug, info, warn, error)")

	cmd.AddCommand(NewVersionCommand(f))
	cmd.AddCommand(NewMCPCommand(f))
	cmd.AddCommand(NewDoctorCommand(f))
	cmd.AddCommand(kwb.NewKnowledgeBaseCommand(f))
	cmd.AddCommand(agentenv.NewAgentEnvCommand(f))

	cmdutil.ConfigureCommands(cmd)

	return cmd
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	factory := cmdutil.NewFactory(ctx)
	cmd := NewGo42Command(ctx, factory)

	_, err := cmd.ExecuteContextC(ctx)
	return cmdutil.ExitCode(err)
}

func initLogging(level string, output io.Writer) {
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	loggerOpts := &tint.Options{
		AddSource:  false,
		Level:      slogLevel,
		TimeFormat: time.TimeOnly,
	}

	logger := slog.New(tint.NewHandler(output, loggerOpts))

	// Any call to log.* will be redirected to slog.Error.
	// Because of that, we need to agree to use `log` package only for errors.
	slog.SetLogLoggerLevel(slog.LevelError)

	// for both 'log' and 'slog'
	slog.SetDefault(logger)
}
