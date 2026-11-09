package generator

import (
	"context"
	"maps"
)

type Context struct {
	ctx  context.Context
	data map[string]any
}

func newContext(ctx context.Context) *Context {
	return &Context{
		ctx:  ctx,
		data: make(map[string]any),
	}
}

func (c *Context) Set(key string, value any) {
	c.data[key] = value
}

func (c *Context) ToMap() map[string]any {
	return maps.Clone(c.data)
}
