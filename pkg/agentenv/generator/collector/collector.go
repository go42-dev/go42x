package collector

import (
	"context"
)

type BaseCollector struct {
	name     string
	priority int
}

func NewBaseCollector(name string, priority int) BaseCollector {
	return BaseCollector{
		name:     name,
		priority: priority,
	}
}

func (b BaseCollector) Name() string {
	return b.name
}

func (b BaseCollector) Priority() int {
	return b.priority
}

func (b BaseCollector) Collect(_ context.Context) (map[string]any, error) {
	return nil, nil
}
