package cmdutil

import (
	"fmt"
	"slices"
)

const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
	LogLevelWarn  = "warn"
	LogLevelError = "error"
)

type Options struct {
	LogLevel string
}

func (o *Options) Validate() error {
	if o == nil {
		return fmt.Errorf("options cannot be nil")
	}
	if !slices.Contains([]string{LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError}, o.LogLevel) {
		return fmt.Errorf("log-level must be debug, info, warn, or error")
	}
	return nil
}
