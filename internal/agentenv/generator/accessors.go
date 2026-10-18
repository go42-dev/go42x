package generator

import (
	"context"

	"github.com/go42-dev/go42x/internal/fspatch"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type collectorAccessor interface {
	Name() string
	Priority() int
	Collect(ctx context.Context) (map[string]any, error)
}

type providerAccessor interface {
	Prepare(plan *fspatch.Plan, ctxData map[string]any) error
}
