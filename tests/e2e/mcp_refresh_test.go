package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go42-dev/go42x/internal/kwb"
)

// mcpClient drives one go42x MCP server over stdio.
type mcpClient struct {
	t       *testing.T
	encoder *json.Encoder
	decoder *json.Decoder
	nextID  int
}

// startMCP starts the server in the project and closes it when the test ends.
func startMCP(t *testing.T, p *project, args ...string) *mcpClient {
	t.Helper()
	// The test context ends before cleanups run, and cleanup must close stdin before the server stops.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
	cmd := p.command(ctx, append([]string{"mcp", "--log-level=debug"}, args...)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer cancel()
		closeErr := stdin.Close()
		if err := cmd.Wait(); err != nil || closeErr != nil {
			t.Errorf("MCP server did not exit cleanly: %v %v", err, closeErr)
		}
		if t.Failed() {
			t.Logf("MCP stderr: %s", &stderr)
		}
	})
	client := &mcpClient{t: t, encoder: json.NewEncoder(stdin), decoder: json.NewDecoder(stdout)}
	var initialized json.RawMessage
	client.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "e2e", "version": "1"},
	}, &initialized)
	if err := client.encoder.Encode(
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
	); err != nil {
		t.Fatal(err)
	}
	return client
}

func (c *mcpClient) request(method string, params, result any) {
	c.t.Helper()
	c.nextID++
	if err := c.encoder.Encode(
		map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params},
	); err != nil {
		c.t.Fatalf("send %s: %v", method, err)
	}
	var response struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := c.decoder.Decode(&response); err != nil || response.ID != c.nextID || len(response.Error) != 0 {
		c.t.Fatalf("%s response: %+v %v", method, response, err)
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		c.t.Fatalf("decode %s result: %v", method, err)
	}
}

// search calls kwb_search and decodes its text result.
func (c *mcpClient) search(query string) kwb.SearchResponse {
	c.t.Helper()
	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	c.request("tools/call", map[string]any{"name": "kwb_search", "arguments": map[string]any{"query": query}}, &called)
	var result kwb.SearchResponse
	if called.IsError || len(called.Content) != 1 || json.Unmarshal([]byte(called.Content[0].Text), &result) != nil {
		c.t.Fatalf("kwb_search %q failed: %+v", query, called)
	}
	return result
}

// awaitSearch polls until a search for query returns one current result.
func (c *mcpClient) awaitSearch(query string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		result := c.search(query)
		if result.Total == 1 && !result.Results[0].Stale {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%q not refreshed within %s: %+v", query, timeout, result)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestMCPRefreshesIndex(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, "guide.md", "# Guide\nalphaword\n")
	p.run(t, 0, "kwb", "build")
	exclusions := p.read(t, ".go42x/kwb.ignore")
	p.write(t, "added.md", "# Added\nbetaword\n")

	client := startMCP(t, p, "--root", p.root)
	client.awaitSearch("betaword", 20*time.Second)

	// An edit during the session returns a stale result first, then a refreshed one.
	p.write(t, "guide.md", "# Guide\nalphaword gammaword\n")
	if result := client.search("alphaword"); result.Total != 1 || !result.Results[0].Stale {
		t.Fatalf("edited file not marked stale: %+v", result)
	}
	client.awaitSearch("gammaword", 30*time.Second)
	if p.read(t, ".go42x/kwb.ignore") != exclusions {
		t.Fatal("server rewrote the exclusion file")
	}
}

func TestMCPIndexRefreshCanBeDisabled(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.write(t, "guide.md", "# Guide\nalphaword\n")
	p.run(t, 0, "kwb", "build")
	p.write(t, "added.md", "# Added\nbetaword\n")

	client := startMCP(t, p, "--root", p.root, "--index-refresh=false")
	// An enabled refresher indexes this small project well within this window.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if result := client.search("betaword"); result.Total != 0 {
			t.Fatalf("disabled refresh indexed a new file: %+v", result)
		}
		time.Sleep(200 * time.Millisecond)
	}
	p.run(t, 1, "kwb", "check")
}
