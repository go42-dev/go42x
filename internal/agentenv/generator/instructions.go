package generator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
)

const (
	budgetInstructionBytes = 24000
	limitInstructionDepth  = 32
)

const (
	limitSourceRuntime = "runtime"
	limitSourcePolicy  = "project policy"
)

const dirWorkflows = ".github/workflows"

const engineShared = "shared"

var (
	instructionImportPattern = regexp.MustCompile(`(?m)^@([^\s]+)[ \t]*$`)
	antigravityImportPattern = regexp.MustCompile(`@\[[^\]\r\n]*\]\(([^)\r\n]+)\)`)
)

type instructionCheck struct {
	engine      string
	source      string
	file        string
	bytes       int
	limitBytes  int
	limitSource string
	warning     string
}

type instructionTarget struct {
	engine string
	source string
	limit  *int
}

type instructionReader struct {
	ctx    context.Context
	root   string
	shared []byte
	sizes  map[string]int
}

// workflowInstructions reads only the fields needed to identify an instruction
// consumer. The gh-aw compiler remains responsible for workflow validation.
type workflowInstructions struct {
	Engine  yaml.Node   `yaml:"engine"`
	Imports []yaml.Node `yaml:"imports"`
	On      yaml.Node   `yaml:"on"`
}

type workflowEngine struct {
	ID     string `yaml:"id"`
	Config string `yaml:"config"`
}

type workflowImport struct {
	Path string `yaml:"path"`
}

// checkInstructions measures rendered shared instructions and their local imports.
// Limits cover project-owned files; personal instructions and aggregate token
// budgets cannot be established from the project configuration alone.
func checkInstructions(
	ctx context.Context,
	root string,
	cfg *config.Config,
	shared []byte,
) ([]instructionCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	targets, err := instructionTargets(root, cfg)
	if err != nil {
		return nil, err
	}

	reader := &instructionReader{
		ctx:    ctx,
		root:   root,
		shared: shared,
	}
	checks := make([]instructionCheck, 0, len(targets))
	for _, target := range targets {
		limit, source, err := instructionLimit(target)
		if err != nil {
			return nil, err
		}

		reader.sizes = make(map[string]int)
		_, err = reader.measure(provider.AgentsFile, target.engine, make(map[string]bool))
		if err != nil {
			return nil, fmt.Errorf("check instructions for %s: %w", target.source, err)
		}

		for _, path := range slices.Sorted(maps.Keys(reader.sizes)) {
			check := instructionCheck{
				engine:      target.engine,
				source:      target.source,
				file:        path,
				bytes:       reader.sizes[path],
				limitBytes:  limit,
				limitSource: source,
			}
			if check.bytes > check.limitBytes {
				check.warning = "instructions exceed the size limit and may not load completely"
			}
			checks = append(checks, check)
		}
	}

	return checks, nil
}

func instructionTargets(root string, cfg *config.Config) ([]instructionTarget, error) {
	var targets []instructionTarget
	for _, name := range slices.Sorted(maps.Keys(cfg.Providers)) {
		if !cfg.ProviderEnabled(name) || name == ghaw.Name {
			continue
		}

		target := instructionTarget{
			engine: name,
			source: filepath.Join(config.Directory, config.FileProject),
		}
		if name == codex.Name {
			target.limit = cfg.Providers[name].ProjectDocMaxBytes
			if target.limit == nil {
				limit, err := codex.InstructionLimit(root)
				if err != nil {
					return nil, err
				}
				target.limit = &limit
			}
		}
		targets = append(targets, target)
	}

	workflows, err := workflowInstructionTargets(root)
	if err != nil {
		return nil, err
	}
	targets = append(targets, workflows...)
	if len(targets) == 0 {
		targets = append(targets, instructionTarget{
			engine: engineShared,
			source: filepath.Join(config.Directory, config.FileProject),
		})
	}

	return targets, nil
}

func instructionLimit(target instructionTarget) (int, string, error) {
	switch target.engine {
	case codex.Name:
		if target.limit != nil {
			return *target.limit, limitSourceRuntime, nil
		}
		return codex.DefaultInstructionBytes, limitSourceRuntime, nil
	case claude.Name:
		return claude.MaxInstructionBytes, limitSourceRuntime, nil
	case antigravity.Name:
		return antigravity.MaxInstructionBytes, limitSourceRuntime, nil
	case copilot.Name, crush.Name, engineShared:
		return budgetInstructionBytes, limitSourcePolicy, nil
	default:
		return 0, "", fmt.Errorf("cannot determine instruction limit for engine %q in %s", target.engine, target.source)
	}
}

func workflowInstructionTargets(root string) ([]instructionTarget, error) {
	entries, err := os.ReadDir(filepath.Join(root, dirWorkflows))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workflow directory: %w", err)
	}

	var targets []instructionTarget
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		path := filepath.Join(dirWorkflows, entry.Name())
		if path == filepath.FromSlash(ghaw.MCPFile) {
			continue
		}
		settings, err := readWorkflowInstructions(root, path)
		if err != nil {
			return nil, err
		}
		if settings == nil || settings.On.Kind == 0 {
			continue
		}

		engine, err := resolveWorkflowEngine(root, path, make(map[string]bool))
		if err != nil {
			return nil, err
		}
		if len(engine.ID) == 0 {
			// gh-aw defaults to Copilot when neither the workflow nor its imports select an engine.
			engine.ID = copilot.Name
		}
		if engine.ID == antigravity.EngineAlias {
			engine.ID = antigravity.Name
		}

		var limit *int
		if engine.ID == codex.Name && len(engine.Config) > 0 {
			limit, err = codex.ParseInstructionLimit(engine.Config)
			if err != nil {
				return nil, fmt.Errorf("check engine configuration in %s: %w", path, err)
			}
		}
		targets = append(targets, instructionTarget{
			engine: engine.ID,
			source: path,
			limit:  limit,
		})
	}
	return targets, nil
}

func readWorkflowInstructions(root, path string) (*workflowInstructions, error) {
	// #nosec G304 -- Workflow sources and their local imports select the configuration to inspect.
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, fmt.Errorf("read workflow %s: %w", path, err)
	}

	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	frontmatter, exists := strings.CutPrefix(text, "---\n")
	if !exists {
		return nil, nil
	}
	frontmatter, _, exists = strings.Cut(frontmatter, "\n---")
	if !exists {
		return nil, fmt.Errorf("workflow %s has unterminated front matter", path)
	}

	var settings workflowInstructions
	if err := yaml.Unmarshal([]byte(frontmatter), &settings); err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", path, err)
	}
	return &settings, nil
}

func resolveWorkflowEngine(root, path string, visiting map[string]bool) (workflowEngine, error) {
	if visiting[path] {
		return workflowEngine{}, fmt.Errorf("workflow import cycle at %s", path)
	}
	visiting[path] = true
	defer delete(visiting, path)

	settings, err := readWorkflowInstructions(root, path)
	if err != nil {
		return workflowEngine{}, err
	}
	if settings == nil {
		return workflowEngine{}, nil
	}

	var resolved workflowEngine
	for _, entry := range settings.Imports {
		var imported workflowImport
		if entry.Kind == yaml.ScalarNode {
			imported.Path = entry.Value
		} else if err := entry.Decode(&imported); err != nil {
			return resolved, fmt.Errorf("read workflow import in %s: %w", path, err)
		}
		importPath, _, _ := strings.Cut(imported.Path, "#")
		if len(importPath) == 0 || filepath.Ext(importPath) != ".md" {
			return resolved, fmt.Errorf("cannot resolve workflow engine from an unsupported import in %s", path)
		}
		if strings.Contains(importPath, "@") || strings.Contains(importPath, ":") || filepath.IsAbs(importPath) {
			return resolved, fmt.Errorf("cannot check remote workflow imports in %s", path)
		}
		importPath = filepath.Clean(filepath.Join(filepath.Dir(path), importPath))
		if !filepath.IsLocal(importPath) {
			return resolved, fmt.Errorf("workflow import must stay inside the project: %s", importPath)
		}
		if importPath == filepath.FromSlash(ghaw.MCPFile) {
			// The managed fragment contains only MCP declarations and may not exist until generation finishes.
			continue
		}

		engine, err := resolveWorkflowEngine(root, importPath, visiting)
		if err != nil {
			return resolved, err
		}
		if len(engine.ID) > 0 {
			if len(resolved.ID) > 0 && resolved.ID != engine.ID {
				return resolved, fmt.Errorf("conflicting imported engines in %s", path)
			}
			resolved = engine
		}
	}

	if settings.Engine.Kind == yaml.ScalarNode {
		resolved = workflowEngine{ID: settings.Engine.Value}
	} else if settings.Engine.Kind != 0 {
		var engine workflowEngine
		if err := settings.Engine.Decode(&engine); err != nil {
			return resolved, fmt.Errorf("read workflow engine in %s: %w", path, err)
		}
		if len(engine.ID) > 0 {
			resolved.ID = engine.ID
		}
		if len(engine.Config) > 0 {
			resolved.Config = engine.Config
		}
	}
	return resolved, nil
}

func (r *instructionReader) measure(path, engine string, visiting map[string]bool) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	path = filepath.Clean(path)
	if visiting[path] {
		return 0, fmt.Errorf("instruction import cycle at %s", path)
	}
	if len(visiting) >= limitInstructionDepth {
		return 0, fmt.Errorf("instruction imports exceed %d levels at %s", limitInstructionDepth, path)
	}
	if size, exists := r.sizes[path]; exists {
		return size, nil
	}
	visiting[path] = true
	defer delete(visiting, path)

	content := r.shared
	if path != provider.AgentsFile {
		// #nosec G304 -- Instruction imports explicitly select files whose sizes must be checked.
		var err error
		content, err = os.ReadFile(filepath.Join(r.root, path))
		if err != nil {
			return 0, fmt.Errorf("read instruction file %s: %w", path, err)
		}
	}
	if engine == antigravity.Name && path != provider.AgentsFile {
		text := strings.ReplaceAll(string(content), "\r\n", "\n")
		if body, exists := strings.CutPrefix(text, "---\n"); exists {
			if _, body, exists = strings.Cut(body, "\n---\n"); exists {
				content = []byte(body)
			}
		}
	}
	size := len(content)
	if engine != claude.Name && engine != copilot.Name && engine != antigravity.Name {
		r.sizes[path] = size
		return size, nil
	}

	pattern := instructionImportPattern
	if engine == antigravity.Name {
		pattern = antigravityImportPattern
	}
	fenced := false
	for line := range strings.SplitAfterSeq(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
		}
		if fenced {
			continue
		}

		for _, match := range pattern.FindAllStringSubmatchIndex(line, -1) {
			importPath := line[match[2]:match[3]]
			if filepath.IsAbs(importPath) || strings.HasPrefix(importPath, "~") {
				return 0, fmt.Errorf("cannot check non-project instruction import %q", importPath)
			}
			importPath = filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(importPath)))
			if !filepath.IsLocal(importPath) {
				return 0, fmt.Errorf("instruction import must stay inside the project: %s", importPath)
			}

			included, err := r.measure(importPath, engine, visiting)
			if err != nil {
				return 0, err
			}
			// Claude's startup cap applies to each file, including imported files.
			if engine != claude.Name {
				size += included - (match[1] - match[0])
			}
		}
	}
	r.sizes[path] = size
	return size, nil
}
