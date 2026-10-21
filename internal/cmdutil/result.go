package cmdutil

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

// ExitError separates process status from Cobra's error-display policy.
type ExitError struct {
	Code     int
	Err      error
	Reported bool
}

type failureResult struct {
	Error  string `json:"error"`
	Result any    `json:"result"`
}

func (e *ExitError) Error() string {
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func UsageError(err error) error {
	if err == nil {
		return nil
	}

	return &ExitError{
		Code: 2,
		Err:  err,
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	if exit, ok := errors.AsType[*ExitError](err); ok {
		return exit.Code
	}

	return 1
}

func JSON(output io.Writer, value any) error {
	return json.NewEncoder(output).Encode(value)
}

// Report writes one result. A nil writeText selects JSON. Runtime errors use an
// error object with any partial result; errors marked Reported retain the
// feature's diagnostic result. Text renderers may show recovery information on
// failure, while Cobra prints the error on stderr.
func Report(output io.Writer, result any, runErr error, writeText func(io.Writer) error) error {
	var exit *ExitError
	reported := errors.As(runErr, &exit) && exit.Reported
	if writeText != nil {
		if err := writeText(output); err != nil {
			return err
		}

		return runErr
	}
	if runErr != nil && !reported {
		failure := failureResult{
			Error:  runErr.Error(),
			Result: result,
		}
		if err := JSON(output, failure); err != nil {
			return err
		}

		return ReportedError(runErr)
	}

	if err := JSON(output, result); err != nil {
		return err
	}

	return runErr
}

// ReportedError prevents Cobra from printing a failure already in the result.
func ReportedError(err error) error {
	if err == nil {
		return nil
	}

	return &ExitError{
		Code:     ExitCode(err),
		Err:      err,
		Reported: true,
	}
}

// ConfigureCommands classifies argument/flag errors and suppresses duplicate
// output for errors already reported by a runner. Call after adding all commands.
func ConfigureCommands(cmd *cobra.Command) {
	if cmd.Args != nil {
		args := cmd.Args
		cmd.Args = func(c *cobra.Command, values []string) error {
			return UsageError(args(c, values))
		}
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return UsageError(err)
	})
	if cmd.RunE != nil {
		run, silenceErrors := cmd.RunE, cmd.SilenceErrors
		cmd.RunE = func(c *cobra.Command, args []string) error {
			c.SilenceErrors = silenceErrors
			err := run(c, args)
			var exit *ExitError
			if errors.As(err, &exit) && exit.Reported {
				c.SilenceErrors = true
			}
			return err
		}
	}
	for _, child := range cmd.Commands() {
		ConfigureCommands(child)
	}
}
