package generator

import (
	"context"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
)

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type collectorAccessor interface {
	Name() string
	Priority() int
	Collect(ctx context.Context) (map[string]interface{}, error)
}

type providerAccessor interface {
	Generate(ctxData map[string]interface{}, cfg config.Provider) error
}
