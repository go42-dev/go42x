package kwb

import "context"

//go:generate mockgen -source $GOFILE -package mocks -destination mocks/mocks.go

// indexAccessor owns persisted generations and their read/write lifecycle.
type indexAccessor interface {
	BuildIndex(ctx context.Context, rootPath string) (BuildResult, error)
	Search(ctx context.Context, options SearchOptions) (*SearchResponse, error)
	GetFile(ctx context.Context, path string, startLine, endLine int) (*FileContent, error)
	ListFiles(ctx context.Context, options ListOptions) (*FilesResponse, error)
	GetStats(ctx context.Context) (*Stats, error)
	ProjectRoot() (string, error)
	CloseIndex() error
}

// catalogAccessor reads live documentation for the service's resolved project root.
type catalogAccessor interface {
	Load(ctx context.Context, rootPath, entrypoint string) (*Catalog, error)
}
