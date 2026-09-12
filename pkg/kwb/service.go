package kwb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
)

type Service struct {
	logger       *slog.Logger
	settings     *Settings
	indexManager *indexManager
}

func NewService(settings *Settings, opts ...Option) (*Service, error) {
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}
	copy := *settings
	copy.ExtraExtensions = slices.Clone(settings.ExtraExtensions)
	copy.ExcludeDirs = slices.Clone(settings.ExcludeDirs)
	var err error
	copy.RootPath, err = canonicalPath(copy.RootPath)
	if err != nil {
		return nil, err
	}
	copy.IndexPath, err = canonicalPath(copy.IndexPath)
	if err != nil {
		return nil, err
	}
	svc := &Service{settings: &copy}
	for _, opt := range opts {
		opt(svc)
	}
	if svc.logger == nil {
		svc.logger = slog.New(slog.DiscardHandler)
	}
	svc.indexManager = newIndexManager(svc.settings, svc.logger.With("component", "index_manager"))
	return svc, nil
}

func (s *Service) BuildIndex(ctx context.Context, rootPath string) (BuildResult, error) {
	report, err := s.indexManager.BuildIndex(ctx, rootPath)
	if err != nil {
		return report, fmt.Errorf("building index: %w", err)
	}
	s.logger.InfoContext(ctx, "Knowledge base updated", "indexed", report.Indexed, "unchanged", report.Unchanged,
		"deleted", report.Deleted, "files", report.Files, "chunks", report.Chunks, "duration", report.Duration)
	return report, nil
}

func (s *Service) Search(ctx context.Context, options SearchOptions) (*SearchResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	return s.indexManager.Search(ctx, options)
}

func (s *Service) GetFile(ctx context.Context, path string, startLine, endLine int) (*FileContent, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	return s.indexManager.GetFile(ctx, path, startLine, endLine)
}

func (s *Service) ListFiles(ctx context.Context, options ListOptions) (*FilesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	return s.indexManager.ListFiles(ctx, options)
}

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, s.settings.SearchTimeout)
	defer cancel()
	return s.indexManager.GetStats(ctx)
}

func (s *Service) Close() error { return s.indexManager.CloseIndex() }

// Resolve existing symlinked ancestors even when the final index path does not
// exist yet, so source walking and index exclusion use the same absolute paths.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}
	parent, err = canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}
