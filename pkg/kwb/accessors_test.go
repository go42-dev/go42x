package kwb_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/pkg/kwb"
	"github.com/go42-dev/go42x/pkg/kwb/mocks"
)

func TestContextPropagatesCatalogFailure(t *testing.T) {
	settings := kwb.NewSettings()
	settings.RootPath = t.TempDir()
	settings.IndexPath = filepath.Join(settings.RootPath, settings.IndexPath)
	ctrl := gomock.NewController(t)
	index := mocks.NewMockindexAccessor(ctrl)
	catalog := mocks.NewMockcatalogAccessor(ctrl)
	failure := errors.New("catalog unavailable")
	index.EXPECT().ProjectRoot().Return(settings.RootPath, nil)
	index.EXPECT().CloseIndex().Return(nil)
	catalog.EXPECT().Load(gomock.Any(), settings.RootPath, settings.Entrypoint).Return(nil, failure)
	service := kwb.NewServiceForTest(t, settings, index, catalog)
	result, err := service.Context(t.Context(), kwb.ContextOptions{
		Task: "rotate tokens",
	})
	if result != nil || !errors.Is(err, failure) {
		t.Fatalf("catalog failure: result=%+v err=%v", result, err)
	}
}

func TestContextHandlesIndexFailures(t *testing.T) {
	settings := kwb.NewSettings()
	settings.RootPath = t.TempDir()
	settings.IndexPath = filepath.Join(settings.RootPath, settings.IndexPath)
	settings.RequireRootMatch = true
	entrypoint := filepath.Join(settings.RootPath, settings.Entrypoint)
	if err := os.MkdirAll(filepath.Dir(entrypoint), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("# Documentation\n"), 0600); err != nil {
		t.Fatal(err)
	}
	live, err := kwb.NewService(settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	root, err := live.ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := live.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		prepare    func(*mocks.MockindexAccessor)
		diagnostic string
		failure    error
	}{
		{
			name: "root unavailable with explicit root",
			prepare: func(index *mocks.MockindexAccessor) {
				index.EXPECT().ProjectRoot().Return("", os.ErrNotExist)
				index.EXPECT().GetStats(gomock.Any()).Return(nil, os.ErrNotExist)
			},
			diagnostic: "index_unavailable",
		},
		{
			name: "index unavailable",
			prepare: func(index *mocks.MockindexAccessor) {
				index.EXPECT().ProjectRoot().Return(root, nil)
				index.EXPECT().GetStats(gomock.Any()).Return(nil, os.ErrNotExist)
			},
			diagnostic: "index_unavailable",
		},
		{
			name: "root mismatch",
			prepare: func(index *mocks.MockindexAccessor) {
				index.EXPECT().ProjectRoot().Return("", kwb.ErrRootMismatch)
			},
			failure: kwb.ErrRootMismatch,
		},
		{
			name: "root changed during retrieval",
			prepare: func(index *mocks.MockindexAccessor) {
				index.EXPECT().ProjectRoot().Return(root, nil)
				index.EXPECT().GetStats(gomock.Any()).Return(&kwb.Stats{
					RootPath:   "another-root",
					Generation: "gen-test",
				}, nil)
			},
			failure: kwb.ErrRootMismatch,
		},
		{
			name: "search failure",
			prepare: func(index *mocks.MockindexAccessor) {
				index.EXPECT().ProjectRoot().Return(root, nil)
				index.EXPECT().GetStats(gomock.Any()).Return(&kwb.Stats{
					RootPath:   root,
					Generation: "gen-test",
				}, nil)
				index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, errors.New("search unavailable"))
			},
			diagnostic: "search_failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			index := mocks.NewMockindexAccessor(ctrl)
			documentation := mocks.NewMockcatalogAccessor(ctrl)
			if test.name != "root mismatch" {
				documentation.EXPECT().Load(gomock.Any(), root, settings.Entrypoint).Return(catalog, nil)
			}
			test.prepare(index)
			index.EXPECT().CloseIndex().Return(nil)
			service := kwb.NewServiceForTest(t, settings, index, documentation)
			result, err := service.Context(t.Context(), kwb.ContextOptions{
				Task: "rotate tokens",
			})
			if test.failure != nil {
				if result != nil || !errors.Is(err, test.failure) {
					t.Fatalf("result=%+v err=%v, want %v", result, err, test.failure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(result.Diagnostics, func(diagnostic kwb.Diagnostic) bool {
				return diagnostic.Code == test.diagnostic
			}) {
				t.Fatalf("result=%+v, want diagnostic %s", result, test.diagnostic)
			}
			if len(result.Items) == 0 || result.Items[0].Path != settings.Entrypoint ||
				result.Items[0].Source.Content != "# Documentation\n" {
				t.Fatalf("documentation unavailable after index failure: %+v", result)
			}
		})
	}
}
