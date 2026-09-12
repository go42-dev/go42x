package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/go42-dev/go42x/internal/version"
)

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Version information",
		Long:  `Version information`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersionCommand(cmd)
		},
	}
	return cmd
}

func runVersionCommand(_ *cobra.Command) error {
	fmt.Printf("Version: %s\n", version.GetVersion())
	fmt.Printf("Go:      %s\n", runtime.Version())
	fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}
