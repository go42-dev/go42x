package provider

//go:generate go tool mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

type TemplateEngineAccessor interface {
	Process(template string, ctxData map[string]any) (string, error)
}
