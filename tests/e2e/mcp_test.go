package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPStdioSession(t *testing.T) {
	t.Parallel()
	for _, tt := range []mcpSessionConfig{
		{name: "default without configuration"},
		{
			name:       "project configuration",
			entrypoint: "handbook/start.md",
		},
		{
			name:            "local override",
			entrypoint:      "handbook/shared.md",
			localEntrypoint: "team/start.markdown",
		},
		{
			name:        "indexed project root",
			entrypoint:  "handbook/start.md",
			indexedRoot: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runMCPStdioSession(t, false, tt)
		})
	}
}

func TestMCPReadTimeouts(t *testing.T) {
	t.Parallel()
	runMCPStdioSession(t, true, mcpSessionConfig{})
}

type mcpSessionConfig struct {
	name, entrypoint, localEntrypoint string
	indexedRoot                       bool
}

func TestMCPDocumentationEntrypointFlagRemoved(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	result := p.run(t, 2, "mcp", "--docs-entrypoint=handbook/start.md")
	if !strings.Contains(result.stderr, "unknown flag: --docs-entrypoint") {
		t.Fatalf("removed flag still accepted: %+v", result)
	}
}

func runMCPStdioSession(t *testing.T, expectTimeout bool, config mcpSessionConfig) {
	t.Helper()
	p := newProject(t)
	p.write(t, "README.md", "---\nid: overview\ntitle: Local project\n---\n# Local project\nE2E example.\n")
	const projectConfig = `version: "1.0"
project: {name: example, documentation-entrypoint: %s}
context: {template: agents.tpl.md}
providers: {codex: {enabled: false}}
`
	entrypoint := "docs/README.md"
	wantFiles := 4

	if len(config.entrypoint) > 0 {
		p.write(t, ".go42x/go42x.yaml", fmt.Sprintf(projectConfig, config.entrypoint))
		entrypoint = config.entrypoint
		wantFiles++
	}
	if len(config.localEntrypoint) > 0 {
		p.write(t, ".go42x/go42x.local.yaml", "project: {documentation-entrypoint: "+config.localEntrypoint+"}\n")
		entrypoint = config.localEntrypoint
	}
	p.write(t, entrypoint, "# Project documentation\n")
	p.write(t, filepath.Join(filepath.Dir(entrypoint), "overview.md"),
		"---\nid: overview\ntitle: Local project\n---\n# Local project\nE2E example.\n")
	p.run(t, 0, "kwb", "build")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	args := []string{"mcp", "--log-level=debug"}

	if config.indexedRoot {
		args = append(args, "--index", filepath.Join(p.root, ".go42x/kwb/index"))
	} else {
		args = append(args, "--root", p.root)
	}

	p.env = append(p.env, "GO42X_CONTEXT_DOC=missing-env-guidance", "GO42X_DOCS_ENTRYPOINT=ignored.md")
	args = append(args, "--context-doc=overview")

	if expectTimeout {
		args = append(args, "--search-timeout=1ns")
	}
	cmd := p.command(ctx, args...)
	other := newProject(t)
	other.write(t, ".go42x/go42x.yaml", fmt.Sprintf(projectConfig, "wrong-project.md"))
	cmd.Dir = other.root
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
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
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
		"clientInfo": map[string]string{
			"name":    "e2e",
			"version": "1",
		},
	}, &initialized)
	if initialized.ProtocolVersion != "2024-11-05" || initialized.ServerInfo.Name != "go42x" ||
		initialized.ServerInfo.Version != binaryVersion {
		t.Fatalf("unexpected server initialization: %+v", initialized)
	}
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}); err != nil {
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
			{
				"kwb_search",
				map[string]any{"query": "project"},
			},
			{
				"kwb_get_file",
				map[string]any{"path": "README.md"},
			},
			{
				"kwb_list_files",
				map[string]any{},
			},
			{
				"kwb_stats",
				map[string]any{},
			},
			{
				"docs_get",
				map[string]any{"id": "overview"},
			},
			{
				"docs_impact",
				map[string]any{"paths": []string{"README.md"}},
			},
			{
				"project_context",
				map[string]any{},
			},
		} {
			called.IsError, called.Content = false, nil
			request(i+3, "tools/call", map[string]any{
				"name":      tool.name,
				"arguments": tool.args,
			}, &called)
			if !called.IsError || len(called.Content) != 1 || called.Content[0].Type != "text" ||
				!strings.Contains(called.Content[0].Text, "context deadline exceeded") {
				t.Errorf("%s did not report its read timeout: %+v", tool.name, called)
			}
		}
	} else {
		request(3, "tools/call", map[string]any{
			"name":      "kwb_stats",
			"arguments": map[string]any{},
		}, &called)
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
		if stats.DocumentCount != wantFiles || stats.ChunkCount < wantFiles || len(stats.Generation) == 0 {
			t.Fatalf("kwb_stats does not describe the indexed project: %+v", stats)
		}
		request(
			4,
			"tools/call",
			map[string]any{
				"name":      "project_context",
				"arguments": map[string]any{},
			},
			&called,
		)
		if called.IsError || len(called.Content) != 1 ||
			!strings.Contains(called.Content[0].Text, "project_guidance") ||
			strings.Contains(called.Content[0].Text, "guidance_unavailable") {
			t.Fatalf("configured guidance or flag precedence lost: %+v", called)
		}
		var result struct {
			Items       []struct{ Path, Reason string } `json:"items"`
			Diagnostics []struct{ Code string }         `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(called.Content[0].Text), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) == 0 || result.Items[0].Path != entrypoint || result.Items[0].Reason != "bootstrap" {
			t.Fatalf("MCP ignored the project's documentation entrypoint: %+v", result.Items)
		}
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Code == "docs_entrypoint_missing" {
				t.Fatalf("MCP did not load the configured entrypoint: %+v", result.Diagnostics)
			}
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
