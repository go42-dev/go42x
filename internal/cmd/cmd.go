package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/cmd/agentenv"
	"github.com/go42-dev/go42x/internal/cmd/kwb"
	"github.com/go42-dev/go42x/internal/cmdutil"
)

const (
	exitOK    = 0
	exitError = 1
)

func NewGo42Command(ctx context.Context, f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "go42x",
		Short: "Helper tool for go42 project",
		Long:  `Helper tool for go42 project`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			initLogging(f.Options().LogLevel)
		},
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	cmd.SetContext(ctx)
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)

	f.BindFlags(cmd.PersistentFlags())

	cmd.AddCommand(NewVersionCommand())
	cmd.AddCommand(agentenv.NewAgentEnvCommand(f))
	cmd.AddCommand(kwb.NewKnowledgeBaseCommand(f))

	return cmd
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	factory := cmdutil.NewFactory(ctx)
	cmd := NewGo42Command(ctx, factory)

	var execErr error
	cmd, execErr = cmd.ExecuteContextC(ctx)

	if execErr != nil {
		if cmd != nil && cmd.SilenceErrors {
			return exitOK
		}
		return exitError
	}

	return exitOK
}

func initLogging(level string) {
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

	logger := slog.New(tint.NewHandler(os.Stdout, loggerOpts))

	// Any call to log.* will be redirected to slog.Error.
	// Because of that, we need to agree to use `log` package only for errors.
	slog.SetLogLoggerLevel(slog.LevelError)

	// for both 'log' and 'slog'
	slog.SetDefault(logger)
}
