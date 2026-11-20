package source

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSecret = "credential-test-secret"

func lookupFrom(values map[string]string) LookupFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// headerRecorder serves an empty response and keeps the last Authorization header.
type headerRecorder struct {
	mu       sync.Mutex
	header   string
	requests int
}

func (h *headerRecorder) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.header = r.Header.Get(headerAuthorization)
	h.requests++
}

func (h *headerRecorder) last() (string, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.header, h.requests
}

func get(t *testing.T, client *http.Client, target string) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestAuthorizeWithoutAuthKeepsClient(t *testing.T) {
	t.Parallel()
	client := NewHTTPClient(time.Second)
	authorized, err := Authorize(
		client,
		Config{
			Type: TypeHTTP,
			URL:  "https://example.com/file",
		},
		lookupFrom(nil),
	)
	if err != nil || authorized != client {
		t.Fatalf("Authorize = (%p, %v), want the original client", authorized, err)
	}
}

func TestAuthorizeHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		auth Auth
		want string
	}{
		{
			name: "bearer",
			auth: Auth{TokenEnv: "SITE_TOKEN"},
			want: "Bearer " + testSecret,
		},
		{
			name: "basic",
			auth: Auth{
				TokenEnv: "SITE_TOKEN",
				UserEnv:  "SITE_USER",
			},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("reader@example.com:"+testSecret)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := &headerRecorder{}
			server := httptest.NewServer(recorder)
			defer server.Close()

			client, err := Authorize(
				NewHTTPClient(time.Second),
				Config{
					Type: TypeHTTP,
					URL:  server.URL + "/file",
					Auth: tc.auth,
				},
				lookupFrom(map[string]string{
					"SITE_TOKEN": testSecret,
					"SITE_USER":  "reader@example.com",
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			get(t, client, server.URL+"/file")
			if got, _ := recorder.last(); got != tc.want {
				t.Fatalf("Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthorizeSendsCredentialsOnlyToOrigin(t *testing.T) {
	t.Parallel()
	other := &headerRecorder{}
	otherServer := httptest.NewServer(other)
	defer otherServer.Close()

	origin := &headerRecorder{}
	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.ServeHTTP(w, r)
		http.Redirect(w, r, otherServer.URL+"/file", http.StatusFound)
	}))
	defer originServer.Close()

	client, err := Authorize(
		NewHTTPClient(time.Second),
		Config{
			Type: TypeHTTP,
			URL:  originServer.URL + "/file",
			Auth: Auth{TokenEnv: "SITE_TOKEN"},
		},
		lookupFrom(map[string]string{"SITE_TOKEN": testSecret}),
	)
	if err != nil {
		t.Fatal(err)
	}
	get(t, client, originServer.URL+"/file")
	if got, _ := origin.last(); got != "Bearer "+testSecret {
		t.Fatalf("origin Authorization = %q, want the bearer token", got)
	}
	if got, requests := other.last(); requests != 1 || len(got) > 0 {
		t.Fatalf("redirect target got %d requests with Authorization %q, want one without credentials", requests, got)
	}
}

func TestAuthorizeRejectsMissingOrUnsafeCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		url    string
		values map[string]string
		want   string
	}{
		{
			name:   "missing token",
			url:    "https://example.com/file",
			values: map[string]string{"SITE_USER": "reader"},
			want:   "SITE_TOKEN is not set",
		},
		{
			name: "empty token",
			url:  "https://example.com/file",
			values: map[string]string{
				"SITE_TOKEN": "",
				"SITE_USER":  "reader",
			},
			want: "SITE_TOKEN is not set",
		},
		{
			name: "missing user",
			url:  "https://example.com/file",
			values: map[string]string{
				"SITE_TOKEN": testSecret,
			},
			want: "SITE_USER is not set",
		},
		{
			name: "control character",
			url:  "https://example.com/file",
			values: map[string]string{
				"SITE_TOKEN": testSecret + "\r\nX-Injected: 1",
				"SITE_USER":  "reader",
			},
			want: "SITE_TOKEN contains control characters",
		},
		{
			name: "plain HTTP",
			url:  "http://example.com/file",
			values: map[string]string{
				"SITE_TOKEN": testSecret,
				"SITE_USER":  "reader",
			},
			want: "requires an HTTPS URL",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Authorize(
				NewHTTPClient(time.Second),
				Config{
					Type: TypeHTTP,
					URL:  tc.url,
					Auth: Auth{
						TokenEnv: "SITE_TOKEN",
						UserEnv:  "SITE_USER",
					},
				},
				lookupFrom(tc.values),
			)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Authorize error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Fatalf("Authorize error exposes the credential: %v", err)
			}
		})
	}
}
