package kwb_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/mocks"
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
	catalog.EXPECT().Load(gomock.Any(), settings.RootPath).Return(nil, failure)
	service := kwb.NewServiceForTest(t, settings, index, catalog)
	result, err := service.Context(t.Context(), kwb.ContextOptions{})
	if result != nil || !errors.Is(err, failure) {
		t.Fatalf("catalog failure: result=%+v err=%v", result, err)
	}
}

func TestContextReadsGuidanceWithoutIndexQueries(t *testing.T) {
	settings := kwb.NewSettings()
	settings.RootPath = t.TempDir()
	settings.IndexPath = filepath.Join(settings.RootPath, settings.IndexPath)
	settings.RequireRootMatch = true
	entrypoint := filepath.Join(settings.RootPath, "docs/README.md")
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
		name      string
		root      string
		rootError error
	}{
		{
			"missing index with explicit root",
			"",
			os.ErrNotExist,
		},
		{
			"available root",
			root,
			nil,
		},
		{
			"root mismatch",
			"",
			kwb.ErrRootMismatch,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			index := mocks.NewMockindexAccessor(ctrl)
			documentation := mocks.NewMockcatalogAccessor(ctrl)
			index.EXPECT().ProjectRoot().Return(test.root, test.rootError)
			index.EXPECT().CloseIndex().Return(nil)
			if !errors.Is(test.rootError, kwb.ErrRootMismatch) {
				documentation.EXPECT().Load(gomock.Any(), root).Return(catalog, nil)
			}
			service := kwb.NewServiceForTest(t, settings, index, documentation)
			result, err := service.Context(t.Context(), kwb.ContextOptions{})

			if errors.Is(test.rootError, kwb.ErrRootMismatch) {
				if result != nil || !errors.Is(err, kwb.ErrRootMismatch) {
					t.Fatalf("root mismatch: result=%+v error=%v", result, err)
				}
				return
			}
			if err != nil || len(result.Diagnostics) != 0 || len(result.Items) != 1 ||
				result.Items[0].Path != "docs/README.md" ||
				result.Items[0].Source.Content != "# Documentation\n" {
				t.Fatalf("live guidance: result=%+v error=%v", result, err)
			}
		})
	}
}
