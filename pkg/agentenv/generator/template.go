package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/go42-dev/go42x/pkg/agentenv/config"
	"github.com/go42-dev/go42x/pkg/agentenv/generator/provider"
)

const (
	chunksPlaceholder    = "{{ .chunks }}"
	modesPlaceholder     = "{{ .modes }}"
	workflowsPlaceholder = "{{ .workflows }}"
)

func defaultTemplateOps() template.FuncMap {
	return template.FuncMap{
		"mcpTool":    provider.MCPToolName,
		"hasMCPTool": hasMCPTool,
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"trim":       strings.TrimSpace,
		"join":       strings.Join,
	}
}

func hasMCPTool(servers map[string]config.MCPServer, server, tool string) bool {
	return slices.Contains(servers[server].Tools, tool)
}

// ---

type templateEngine struct {
	baseDir   string
	functions template.FuncMap
}

func newTemplateEngine(baseDir string) *templateEngine {
	return &templateEngine{
		baseDir:   baseDir,
		functions: defaultTemplateOps(),
	}
}

func (e *templateEngine) Render(cfg config.Context, ctxData map[string]any) (string, error) {
	content, err := e.loadTemplate(cfg.Template)
	if err != nil {
		return "", fmt.Errorf("failed to load template: %w", err)
	}
	for _, section := range []struct {
		name        string
		dir         string
		placeholder string
	}{
		{"chunks", cfg.ChunksDir, chunksPlaceholder},
		{"modes", cfg.ModesDir, modesPlaceholder},
		{"workflows", cfg.WorkflowsDir, workflowsPlaceholder},
	} {
		parts, err := e.loadTemplates(section.dir)
		if err != nil {
			return "", fmt.Errorf("failed to load %s: %w", section.name, err)
		}
		content = e.inject(content, mergeStrings(parts), section.placeholder)
	}
	return e.Process(content, ctxData)
}

func (e *templateEngine) loadTemplate(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(e.baseDir, path))
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", path, err)
	}
	return string(data), nil
}

func (e *templateEngine) loadTemplates(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	// os.ReadDir returns entries sorted by filename.
	entries, err := os.ReadDir(filepath.Join(e.baseDir, dir))
	if err != nil {
		return nil, fmt.Errorf("failed to read template directory %s: %w", dir, err)
	}
	contents := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".tpl.md") {
			continue
		}
		content, err := e.loadTemplate(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		contents = append(contents, content)
	}
	return contents, nil
}

func (e *templateEngine) Process(content string, ctxData map[string]any) (string, error) {
	tmpl, err := template.New("main").Funcs(e.functions).Parse(content)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctxData); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

func (e *templateEngine) InjectChunks(content string, chunks string) string {
	return e.inject(content, chunks, chunksPlaceholder)
}

func (e *templateEngine) InjectModes(content string, modes string) string {
	return e.inject(content, modes, modesPlaceholder)
}

func (e *templateEngine) InjectWorkflows(content string, workflows string) string {
	return e.inject(content, workflows, workflowsPlaceholder)
}

func (e *templateEngine) inject(content string, payload string, placeholder string) string {
	return strings.Replace(content, placeholder, payload, 1)
}

func mergeStrings(items []string) string {
	trimmed := make([]string, len(items))
	for i, item := range items {
		trimmed[i] = strings.TrimSpace(item)
	}
	return strings.Join(trimmed, "\n\n")
}
