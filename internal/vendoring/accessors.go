package vendoring

import (
	"context"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

//go:generate go tool mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type adapterAccessor interface {
	Fetch(context.Context, source.Config, *source.Resolved, string) (source.Result, error)
}

type converterAccessor interface {
	Convert([]byte) ([]byte, error)
}
