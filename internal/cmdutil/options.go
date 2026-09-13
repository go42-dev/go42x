package cmdutil

import (
	"fmt"
	"slices"
)

type Options struct {
	LogLevel string
}

func (o *Options) Validate() error {
	if o == nil {
		return fmt.Errorf("options cannot be nil")
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, o.LogLevel) {
		return fmt.Errorf("log-level must be debug, info, warn, or error")
	}
	return nil
}
