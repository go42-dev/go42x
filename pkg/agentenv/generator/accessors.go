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
	Collect(ctx context.Context) (map[string]any, error)
}

type providerAccessor interface {
	InstructionsFileName() string
	Prepare(plan *output.Plan, ctxData map[string]any, cfg config.Provider) error
}
