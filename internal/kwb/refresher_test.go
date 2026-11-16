package kwb_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/go42-dev/go42x/internal/kwb"
	"github.com/go42-dev/go42x/internal/kwb/mocks"
)

// refreshInterval mirrors the refresher's minimum time between refreshes.
const refreshInterval = 10 * time.Second

func refresherService(t *testing.T) (*kwb.Service, *mocks.MockindexAccessor) {
	t.Helper()
	settings := kwb.NewSettings()
	settings.RootPath = t.TempDir()
	settings.IndexPath = filepath.Join(settings.RootPath, "index")
	ctrl := gomock.NewController(t)
	index, catalog := mocks.NewMockindexAccessor(ctrl), mocks.NewMockcatalogAccessor(ctrl)
	index.EXPECT().CloseIndex().Return(nil)
	return kwb.NewServiceForTest(t, settings, index, catalog), index
}

// countRefreshes counts refresh calls that succeed without changes.
func countRefreshes(index *mocks.MockindexAccessor) *atomic.Int32 {
	var calls atomic.Int32
	index.EXPECT().RefreshIndex(gomock.Any()).DoAndReturn(func(context.Context) (kwb.BuildResult, error) {
		calls.Add(1)
		return kwb.BuildResult{}, nil
	}).AnyTimes()
	return &calls
}

func TestRefresherRunsAtStartAndOnRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, index := refresherService(t)
		calls := countRefreshes(index)
		refresher := service.StartRefresher(t.Context())
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("refreshes at start = %d", calls.Load())
		}

		refresher.Request()
		refresher.Request()
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("refreshed before the interval: %d", calls.Load())
		}
		time.Sleep(refreshInterval)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("combined requests ran %d refreshes in total", calls.Load())
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("refreshed without a request: %d", calls.Load())
		}
		if err := refresher.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRefresherShutdownCancelsRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, index := refresherService(t)
		var canceled atomic.Bool
		index.EXPECT().RefreshIndex(gomock.Any()).DoAndReturn(func(ctx context.Context) (kwb.BuildResult, error) {
			<-ctx.Done()
			canceled.Store(true)
			return kwb.BuildResult{}, ctx.Err()
		})
		refresher := service.StartRefresher(t.Context())
		synctest.Wait()
		if err := refresher.Shutdown(t.Context()); err != nil || !canceled.Load() {
			t.Fatalf("shutdown: %v, refresh canceled: %v", err, canceled.Load())
		}
	})
}

func TestSearchRequestsRefreshForStaleResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, index := refresherService(t)
		calls := countRefreshes(index)
		index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(&kwb.SearchResponse{
			Results: []kwb.SearchResult{{Path: "fresh.md"}, {Path: "edited.md", Stale: true}},
		}, nil)
		refresher := service.StartRefresher(t.Context())
		synctest.Wait()
		if _, err := service.Search(t.Context(), kwb.SearchOptions{Query: "marker"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(refreshInterval)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("stale result did not request a refresh: %d refreshes", calls.Load())
		}
		if err := refresher.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOlderIndexFormatExplainsRebuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service, index := refresherService(t)
		countRefreshes(index)
		index.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, kwb.ErrIndexFormat).Times(2)
		options := kwb.SearchOptions{Query: "marker"}
		if _, err := service.Search(t.Context(), options); !errors.Is(err, kwb.ErrIndexFormat) ||
			strings.Contains(err.Error(), "being rebuilt") {
			t.Fatalf("without refresher: %v", err)
		}

		refresher := service.StartRefresher(t.Context())
		synctest.Wait()
		if _, err := service.Search(t.Context(), options); !errors.Is(err, kwb.ErrIndexFormat) ||
			!strings.Contains(err.Error(), "being rebuilt") {
			t.Fatalf("with refresher: %v", err)
		}
		if err := refresher.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
