package cmdutil

import "github.com/spf13/pflag"

type Options struct {
	LogLevel string
}

func (*Options) BindFlags(f *pflag.FlagSet) {
	f.String("log-level", "info", "Logging level (debug, info, warn, error)")
}
