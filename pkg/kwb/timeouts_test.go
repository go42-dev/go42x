package kwb_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/pkg/kwb"
	"github.com/go42-dev/go42x/pkg/kwb/mocks"
)

func TestReadDeadlinesAndCancellation(t *testing.T) {
	for _, operation := range []string{"search", "file", "list", "stats", "catalog", "document", "impact", "context"} {
		for _, cancellation := range []string{"configured timeout", "earlier caller deadline", "caller cancellation"} {
			t.Run(operation+"/"+cancellation, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					settings := kwb.NewSettings()
					settings.RootPath = t.TempDir()
					settings.IndexPath = filepath.Join(settings.RootPath, "index")
					settings.SearchTimeout = 5 * time.Second
					ctrl := gomock.NewController(t)
					index, catalog := mocks.NewMockindexAccessor(ctrl), mocks.NewMockcatalogAccessor(ctrl)
					index.EXPECT().CloseIndex().Return(nil)
					service := kwb.NewServiceForTest(t, settings, index, catalog)

					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					started := time.Now()
					wantElapsed := settings.SearchTimeout
					wantDeadline := started.Add(settings.SearchTimeout)
					wantErr := context.DeadlineExceeded
					switch cancellation {
					case "earlier caller deadline":
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, time.Second)
						defer stop()
						wantElapsed = time.Second
						wantDeadline = started.Add(time.Second)
					case "caller cancellation":
						wantElapsed, wantErr = time.Second, context.Canceled
						go func() {
							select {
							case <-time.After(time.Second):
								cancel()
							case <-ctx.Done():
							}
						}()
					}
					wait := func(ctx context.Context) error {
						deadline, ok := ctx.Deadline()
						if !ok || !deadline.Equal(wantDeadline) {
							t.Errorf("read deadline = %v (present %v), want %v", deadline, ok, wantDeadline)
							return errors.New("incorrect read deadline")
						}
						<-ctx.Done()
						return ctx.Err()
					}
					var err error
					switch operation {
					case "search":
						index.EXPECT().Search(gomock.Any(), gomock.Any()).DoAndReturn(
							func(ctx context.Context, _ kwb.SearchOptions) (*kwb.SearchResponse, error) { return nil, wait(ctx) })
						_, err = service.Search(ctx, kwb.SearchOptions{Query: "example"})
					case "file":
						index.EXPECT().GetFile(gomock.Any(), "README.md", 0, 0).DoAndReturn(
							func(ctx context.Context, _ string, _, _ int) (*kwb.FileContent, error) { return nil, wait(ctx) })
						_, err = service.GetFile(ctx, "README.md", 0, 0)
					case "list":
						index.EXPECT().ListFiles(gomock.Any(), gomock.Any()).DoAndReturn(
							func(ctx context.Context, _ kwb.ListOptions) (*kwb.FilesResponse, error) { return nil, wait(ctx) })
						_, err = service.ListFiles(ctx, kwb.ListOptions{})
					case "stats":
						index.EXPECT().GetStats(gomock.Any()).DoAndReturn(
							func(ctx context.Context) (*kwb.Stats, error) { return nil, wait(ctx) })
						_, err = service.GetStats(ctx)
					default:
						index.EXPECT().ProjectRoot().Return(settings.RootPath, nil)
						catalog.EXPECT().Load(gomock.Any(), settings.RootPath, settings.Entrypoint).DoAndReturn(
							func(ctx context.Context, _, _ string) (*kwb.Catalog, error) { return nil, wait(ctx) })
						switch operation {
						case "catalog":
							_, err = service.Catalog(ctx)
						case "document":
							_, err = service.GetDocument(ctx, "overview", 0, 0)
						case "impact":
							_, err = service.Impact(ctx, []string{"README.md"})
						case "context":
							_, err = service.Context(ctx, kwb.ContextOptions{Task: "example"})
						}
					}
					if !errors.Is(err, wantErr) || time.Since(started) != wantElapsed {
						t.Fatalf(
							"read returned %v after %v, want %v after %v",
							err,
							time.Since(started),
							wantErr,
							wantElapsed,
						)
					}
				})
			})
		}
	}
}

func TestContextSharesDeadlineAcrossCatalogAndIndex(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := kwb.NewSettings()
		settings.RootPath = t.TempDir()
		settings.IndexPath = filepath.Join(settings.RootPath, "index")
		settings.SearchTimeout = 5 * time.Second
		ctrl := gomock.NewController(t)
		index, catalog := mocks.NewMockindexAccessor(ctrl), mocks.NewMockcatalogAccessor(ctrl)
		index.EXPECT().CloseIndex().Return(nil)
		service := kwb.NewServiceForTest(t, settings, index, catalog)
		started := time.Now()
		deadline := started.Add(settings.SearchTimeout)
		gomock.InOrder(
			index.EXPECT().ProjectRoot().Return(settings.RootPath, nil),
			catalog.EXPECT().Load(gomock.Any(), settings.RootPath, settings.Entrypoint).DoAndReturn(
				func(ctx context.Context, _, _ string) (*kwb.Catalog, error) {
					if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
						t.Errorf("catalog deadline = %v (present %v), want %v", got, ok, deadline)
					}
					time.Sleep(3 * time.Second)
					return &kwb.Catalog{}, nil
				}),
			index.EXPECT().GetStats(gomock.Any()).DoAndReturn(func(ctx context.Context) (*kwb.Stats, error) {
				if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
					t.Errorf("index deadline = %v (present %v), want original deadline %v", got, ok, deadline)
					return nil, errors.New("read budget was reset")
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}),
		)
		result, err := service.Context(t.Context(), kwb.ContextOptions{Task: "example"})
		if result != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != settings.SearchTimeout {
			t.Fatalf("context result=%+v error=%v elapsed=%v", result, err, time.Since(started))
		}
	})
}
