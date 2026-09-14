package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMCPStdioSession(t *testing.T) {
	t.Parallel()
	runMCPStdioSession(t, false)
}

func TestMCPReadTimeouts(t *testing.T) {
	t.Parallel()
	runMCPStdioSession(t, true)
}

func runMCPStdioSession(t *testing.T, expectTimeout bool) {
	t.Helper()
	p := newProject(t)
	p.write(t, "README.md", "---\nid: overview\ntitle: Local project\n---\n# Local project\nE2E example.\n")
	p.run(t, 0, "kwb", "build")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	args := []string{"mcp", "--root", p.root, "--log-level=debug"}
	if expectTimeout {
		args = append(args, "--search-timeout=1ns")
	}
	cmd := p.command(ctx, args...)
	cmd.Dir = t.TempDir()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
		}
		if t.Failed() {
			t.Logf("MCP stderr: %s", &stderr)
		}
	}()

	// Use the actual stdio protocol so any log line on stdout fails decoding.
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	request := func(id int, method string, params any, result any) {
		t.Helper()
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": method, "params": params,
		}); err != nil {
			t.Fatalf("send %s: %v", method, err)
		}
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("read %s response from stdout: %v", method, err)
		}
		if response.JSONRPC != "2.0" || response.ID != id || len(response.Error) != 0 {
			t.Fatalf("invalid %s response: %+v", method, response)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			t.Fatalf("decode %s result: %v", method, err)
		}
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	request(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "e2e", "version": "1"},
	}, &initialized)
	if initialized.ProtocolVersion != "2024-11-05" || initialized.ServerInfo.Name != "go42x" ||
		initialized.ServerInfo.Version != binaryVersion {
		t.Fatalf("unexpected server initialization: %+v", initialized)
	}
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	request(2, "tools/list", map[string]any{}, &listed)
	names := make(map[string]bool)
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{
		"kwb_search", "kwb_get_file", "kwb_list_files", "kwb_stats", "docs_get", "docs_impact", "project_context",
	} {
		if !names[name] {
			t.Errorf("tool %s was not registered", name)
		}
	}
	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if expectTimeout {
		for i, tool := range []struct {
			name string
			args map[string]any
		}{
			{"kwb_search", map[string]any{"query": "project"}},
			{"kwb_get_file", map[string]any{"path": "README.md"}},
			{"kwb_list_files", map[string]any{}},
			{"kwb_stats", map[string]any{}},
			{"docs_get", map[string]any{"id": "overview"}},
			{"docs_impact", map[string]any{"paths": []string{"README.md"}}},
			{"project_context", map[string]any{"task": "project"}},
		} {
			called.IsError, called.Content = false, nil
			request(i+3, "tools/call", map[string]any{"name": tool.name, "arguments": tool.args}, &called)
			if !called.IsError || len(called.Content) != 1 || called.Content[0].Type != "text" ||
				!strings.Contains(called.Content[0].Text, "context deadline exceeded") {
				t.Errorf("%s did not report its read timeout: %+v", tool.name, called)
			}
		}
	} else {
		request(3, "tools/call", map[string]any{"name": "kwb_stats", "arguments": map[string]any{}}, &called)
		if called.IsError || len(called.Content) != 1 || called.Content[0].Type != "text" ||
			!json.Valid([]byte(called.Content[0].Text)) {
			t.Fatalf("kwb_stats did not return a successful JSON result: %+v", called)
		}
		var stats struct {
			DocumentCount int    `json:"document_count"`
			ChunkCount    int    `json:"chunk_count"`
			Generation    string `json:"generation"`
		}
		if err := json.Unmarshal([]byte(called.Content[0].Text), &stats); err != nil {
			t.Fatal(err)
		}
		if stats.DocumentCount != 1 || stats.ChunkCount < 1 || stats.Generation == "" {
			t.Fatalf("kwb_stats does not describe the indexed project: %+v", stats)
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected stdout after the session: value=%v error=%v", extra, err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || ctx.Err() != nil {
		t.Fatalf("MCP did not exit successfully after stdin closed: %v, context: %v", err, ctx.Err())
	}
}
