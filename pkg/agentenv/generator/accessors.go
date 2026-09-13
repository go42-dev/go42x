package generator

import (
	"context"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/output"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type collectorAccessor interface {
	Name() string
	Priority() int
	// Collect may return partial data alongside an error for optional information.
	Collect(ctx context.Context) (map[string]interface{}, error)
}

type providerAccessor interface {
	Prepare(plan *output.Plan, ctxData map[string]interface{}, cfg config.Provider) error
}
