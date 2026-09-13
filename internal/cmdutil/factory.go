package cmdutil

import (
	"context"
	"io"
	"os"
)

type Factory struct {
	ctx         context.Context
	options     *Options
	input       io.Reader
	output      io.Writer
	errorOutput io.Writer
}

func NewFactory(ctx context.Context) *Factory {
	f := &Factory{
		ctx:         ctx,
		options:     new(Options),
		input:       os.Stdin,
		output:      os.Stdout,
		errorOutput: os.Stderr,
	}
	return f
}

func (f *Factory) Context() context.Context {
	return f.ctx
}

func (f *Factory) Options() *Options {
	return f.options
}

// SetIOStreams supplies the selected command's streams to execution functions.
func (f *Factory) SetIOStreams(input io.Reader, output, errorOutput io.Writer) {
	f.input, f.output, f.errorOutput = input, output, errorOutput
}

func (f *Factory) Input() io.Reader { return f.input }

func (f *Factory) Output() io.Writer { return f.output }

func (f *Factory) ErrorOutput() io.Writer { return f.errorOutput }
