package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
)

const chunksPlaceholder = "{{ .chunks }}"

// instructionTemplate describes one unrendered main template or chunk.
type instructionTemplate struct {
	path    string
	content string
}

// renderedTemplate contains generated text and its source locations.
type renderedTemplate struct {
	content string
	sources *templateSources
}

func defaultTemplateOps() template.FuncMap {
	return template.FuncMap{
		"hasMCPTool": hasMCPTool,
		"yamlValue":  yamlValue,
		"mcpTool":    mcpToolName,
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"trim":       strings.TrimSpace,
		"join":       strings.Join,
	}
}

func hasMCPTool(servers map[string]provider.MCPServer, server, tool string) bool {
	return slices.Contains(servers[server].Tools, tool)
}

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

func (e *templateEngine) Render(cfg config.Context, ctxData map[string]any) (*renderedTemplate, error) {
	main, err := e.loadTemplate(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("failed to load template: %w", err)
	}

	chunks, err := e.loadTemplates(cfg.ChunksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load chunks: %w", err)
	}

	sources := newTemplateSources(main, chunks)
	tmpl, err := e.parse(sources.content)
	if err != nil {
		return nil, err
	}

	var output bytes.Buffer
	if err := sources.track(tmpl, &output); err != nil {
		return nil, fmt.Errorf("track template sources: %w", err)
	}

	if err := tmpl.Execute(&output, ctxData); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return &renderedTemplate{
		content: output.String(),
		sources: sources,
	}, nil
}

func (e *templateEngine) loadTemplate(path string) (instructionTemplate, error) {
	// #nosec G304 -- Template paths are authored in the trusted local project configuration.
	data, err := os.ReadFile(e.resolvePath(path))
	if err != nil {
		return instructionTemplate{}, fmt.Errorf("failed to read template %s: %w", path, err)
	}

	return instructionTemplate{
		path:    filepath.ToSlash(path),
		content: string(data),
	}, nil
}

func (e *templateEngine) loadTemplates(dir string) ([]instructionTemplate, error) {
	if len(dir) == 0 {
		return nil, nil
	}

	// os.ReadDir returns entries sorted by filename.
	entries, err := os.ReadDir(e.resolvePath(dir))
	if err != nil {
		return nil, fmt.Errorf("failed to read template directory %s: %w", dir, err)
	}

	templates := make([]instructionTemplate, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".tpl.md") {
			continue
		}

		chunk, err := e.loadTemplate(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}

		templates = append(templates, chunk)
	}

	return templates, nil
}

func (e *templateEngine) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(e.baseDir, path)
}

func (e *templateEngine) Process(content string, ctxData map[string]any) (string, error) {
	tmpl, err := e.parse(content)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctxData); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

func (e *templateEngine) parse(content string) (*template.Template, error) {
	tmpl := template.New("main").Funcs(e.functions)
	tmpl.Funcs(template.FuncMap{"yamlBlock": yamlBlockFunc(tmpl)})
	if _, err := tmpl.Parse(content); err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	return tmpl, nil
}

func mcpToolName(providerName, serverName, toolName string) string {
	switch providerName {
	case claude.Name:
		return claude.MCPToolName(serverName, toolName)
	case codex.Name:
		return codex.MCPToolName(serverName, toolName)
	case antigravity.Name:
		return antigravity.MCPToolName(serverName, toolName)
	case crush.Name:
		return crush.MCPToolName(serverName, toolName)
	case copilot.Name:
		return copilot.MCPToolName(serverName, toolName)
	case ghaw.Name:
		return ghaw.MCPToolName(serverName, toolName)
	default:
		return toolName
	}
}
