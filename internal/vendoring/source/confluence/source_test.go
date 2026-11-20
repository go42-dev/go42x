package confluence

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go42-dev/go42x/internal/vendoring/source"
)

const (
	testPage   = "42"
	testSecret = "confluence-test-secret"
)

func lookupToken(name string) (string, bool) {
	if name == "PAGE_TOKEN" {
		return testSecret, true
	}

	return "", false
}

func testConfig(server *httptest.Server) source.Config {
	return source.Config{
		Type: source.TypeConfluence,
		URL:  server.URL + "/wiki",
		Page: testPage,
		Auth: source.Auth{TokenEnv: "PAGE_TOKEN"},
	}
}

// adfParagraph returns an ADF document with one paragraph, as JSON text.
func adfParagraph(text string) string {
	return `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
}

func writePage(t *testing.T, w http.ResponseWriter, id string, number int, body string) {
	t.Helper()
	response := map[string]any{
		"id":      id,
		"title":   "Runbook <ops>",
		"version": map[string]int{"number": number},
		"body": map[string]any{
			"atlas_doc_format": map[string]string{
				"representation": "atlas_doc_format",
				"value":          body,
			},
		},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
}

// documentTexts lists top-level blocks of an ADF document as type:text.
func documentTexts(t *testing.T, data []byte) []string {
	t.Helper()
	var doc struct {
		Type    string `json:"type"`
		Content []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Type != "doc" {
		t.Fatalf("page document is not ADF: %v\n%s", err, data)
	}

	var blocks []string
	for _, block := range doc.Content {
		text := ""
		for _, inline := range block.Content {
			text += inline.Text
		}
		blocks = append(blocks, block.Type+":"+text)
	}
	return blocks
}

func TestFetchCurrentAndLockedPage(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/"+testPage || r.URL.Query().Get("body-format") != bodyFormat ||
			r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		mu.Lock()
		queries = append(queries, r.URL.Query().Get("version"))
		mu.Unlock()
		number := 7
		if requested := r.URL.Query().Get("version"); len(requested) > 0 {
			number, _ = strconv.Atoi(requested)
		}
		writePage(t, w, testPage, number, adfParagraph("Version "+strconv.Itoa(number)))
	}))
	defer server.Close()

	adapter := New(source.NewHTTPClient(time.Second), lookupToken)
	result, err := adapter.Fetch(t.Context(), testConfig(server), nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(filepath.Join(result.Root, PageFile))
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolved != (source.Resolved{Revision: "7"}) {
		t.Fatalf("resolved = %+v, want only revision 7", result.Resolved)
	}
	if got := documentTexts(t, document); strings.Join(got, ",") != "heading:Runbook <ops>,paragraph:Version 7" {
		t.Errorf("document blocks = %q, want the title heading and the page paragraph:\n%s", got, document)
	}

	locked := result.Resolved
	if _, err := adapter.Fetch(t.Context(), testConfig(server), &locked, t.TempDir()); err != nil {
		t.Fatalf("locked fetch: %v", err)
	}
	older := source.Resolved{Revision: "6"}
	result, err = adapter.Fetch(t.Context(), testConfig(server), &older, t.TempDir())
	if err != nil {
		t.Fatalf("older locked fetch: %v", err)
	}
	document, err = os.ReadFile(filepath.Join(result.Root, PageFile))
	if err != nil || !strings.Contains(string(document), `"text": "Version 6"`) {
		t.Fatalf("older locked document = %q, %v; want version 6", document, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(queries, ",") != ",7,6" {
		t.Fatalf("version queries = %q, want current, 7, then 6", queries)
	}
}

func TestFetchRejectsInvalidResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		revision string
		respond  func(*testing.T, http.ResponseWriter)
		want     string
	}{
		{
			name: "not found",
			respond: func(_ *testing.T, w http.ResponseWriter) {
				w.WriteHeader(http.StatusNotFound)
			},
			want: "HTTP 404; check the page ID and auth",
		},
		{
			name: "server error",
			respond: func(_ *testing.T, w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadGateway)
			},
			want: "HTTP 502",
		},
		{
			name: "invalid JSON",
			respond: func(_ *testing.T, w http.ResponseWriter) {
				_, _ = w.Write([]byte("<html>"))
			},
			want: "invalid page response",
		},
		{
			name: "different page",
			respond: func(t *testing.T, w http.ResponseWriter) {
				writePage(t, w, "43", 1, adfParagraph("Other"))
			},
			want: "different page",
		},
		{
			name: "missing version",
			respond: func(t *testing.T, w http.ResponseWriter) {
				writePage(t, w, testPage, 0, adfParagraph("Draft"))
			},
			want: "no valid version",
		},
		{
			name:     "different version",
			revision: "5",
			respond: func(t *testing.T, w http.ResponseWriter) {
				writePage(t, w, testPage, 7, adfParagraph("Current"))
			},
			want: "has version 7, want 5",
		},
		{
			name: "missing body",
			respond: func(_ *testing.T, w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"id":"42","version":{"number":1},"body":{}}`))
			},
			want: "no atlas_doc_format body",
		},
		{
			name: "invalid document",
			respond: func(t *testing.T, w http.ResponseWriter) {
				writePage(t, w, testPage, 1, "<p>HTML</p>")
			},
			want: "invalid page document",
		},
		{
			name: "not a document",
			respond: func(t *testing.T, w http.ResponseWriter) {
				writePage(t, w, testPage, 1, `{"type":"paragraph"}`)
			},
			want: "not an ADF document",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.respond(t, w)
			}))
			defer server.Close()

			var locked *source.Resolved
			if len(tc.revision) > 0 {
				locked = &source.Resolved{Revision: tc.revision}
			}
			adapter := New(source.NewHTTPClient(time.Second), lookupToken)
			_, err := adapter.Fetch(t.Context(), testConfig(server), locked, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Fetch error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchWithoutCredentialSendsNoRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	adapter := New(source.NewHTTPClient(time.Second), func(string) (string, bool) {
		return "", false
	})
	_, err := adapter.Fetch(t.Context(), testConfig(server), nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "PAGE_TOKEN is not set") || requests.Load() != 0 {
		t.Fatalf("Fetch error = %v after %d requests, want a missing variable error before any request",
			err, requests.Load())
	}
}
