package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/doctor/check"
)

func TestMCPProbeRejectsRedirects(t *testing.T) {
	for _, protocol := range []string{provider.MCPServerTypeHTTP, provider.MCPServerTypeSSE} {
		for _, status := range []int{
			http.StatusMovedPermanently,
			http.StatusFound,
			http.StatusSeeOther,
			http.StatusTemporaryRedirect,
			http.StatusPermanentRedirect,
		} {
			for _, destination := range []string{"relative", "different-port", "different-hostname"} {
				t.Run(fmt.Sprintf("%s/%d/%s", protocol, status, destination), func(t *testing.T) {
					var redirectedRequests atomic.Int32
					var configuredRequests atomic.Int32
					var credentialsSent atomic.Bool
					target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						redirectedRequests.Add(1)
						w.WriteHeader(http.StatusUnauthorized)
					}))
					defer target.Close()

					location := target.URL
					switch destination {
					case "relative":
						location = "/redirect-target"
					case "different-hostname":
						location = strings.Replace(location, "127.0.0.1", "localhost", 1)
					}
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirect-target" {
							redirectedRequests.Add(1)
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						configuredRequests.Add(1)
						if r.Header.Get("Authorization") == "Bearer synthetic-probe-value" &&
							r.Header.Get("X-Api-Key") == "synthetic-probe-value" {
							credentialsSent.Store(true)
						}
						http.Redirect(w, r, location, status)
					}))
					defer origin.Close()

					result := probeMCP(t.Context(), provider.MCPServer{
						Type: protocol,
						URL:  origin.URL,
						Headers: map[string]string{
							"Authorization": "Bearer synthetic-probe-value",
							"X-Api-Key":     "synthetic-probe-value",
						},
					}, 2*time.Second)
					if configuredRequests.Load() == 0 || !credentialsSent.Load() {
						t.Fatal("probe did not send the configured credentials to the configured endpoint")
					}
					if result.Status != check.StatusFail {
						t.Fatalf("redirected probe status = %v, want failure", result.Status)
					}
					if count := redirectedRequests.Load(); count != 0 {
						t.Fatalf("redirect destination received %d requests, want none", count)
					}
				})
			}
		}
	}
}
