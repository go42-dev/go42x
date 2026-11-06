package generator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/fspatch"
)

const (
	skillFile           = "SKILL.md"
	skillManifestFile   = ".go42x-skills.json"
	maxSkillName        = 64
	maxSkillDescription = 1024
)

// skillNamePattern allows lowercase letters and digits joined by single hyphens.
// @see https://agentskills.io/specification
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type skill struct {
	name string
	// files maps slash-separated paths relative to the skill directory to their content.
	files map[string][]byte
}

type skillMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// skillManifest records the files generation wrote to a provider skill directory,
// so later runs remove only those files and never touch skills added by hand.
type skillManifest struct {
	Files []string `json:"files"`
}

// prepareSkills validates the configured skills and copies them to the skill
// directories of enabled providers. Directories of disabled providers are preserved.
func (g *Generator) prepareSkills(plan *fspatch.Plan, clean bool) error {
	skills, err := loadSkills(g.templateDir, g.config.Context.SkillsDir)
	if err != nil {
		return err
	}

	for _, target := range g.skillTargets() {
		dir := filepath.Join(g.outputDir, filepath.FromSlash(target))
		if err := prepareSkillTarget(plan, dir, skills, clean); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
	}

	return nil
}

// skillTargets lists the project skill directories read by enabled providers.
// Copilot also reads .claude/skills, so it needs the shared directory only without Claude.
func (g *Generator) skillTargets() []string {
	claudeEnabled := g.config.ProviderEnabled(claude.Name)

	var targets []string
	if claudeEnabled {
		targets = append(targets, claude.SkillsDir)
	}
	if g.config.ProviderEnabled(codex.Name) ||
		g.config.ProviderEnabled(antigravity.Name) ||
		(g.config.ProviderEnabled(copilot.Name) && !claudeEnabled) {
		targets = append(targets, provider.SharedSkillsDir)
	}

	return targets
}

// loadSkills reads each immediate subdirectory of dir as a skill. A missing
// directory holds no skills; hidden entries are skipped.
func loadSkills(baseDir, dir string) ([]skill, error) {
	if len(dir) == 0 {
		return nil, nil
	}

	root := dir
	if !filepath.IsAbs(root) {
		root = filepath.Join(baseDir, root)
	}

	// os.ReadDir returns entries sorted by filename.
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read skill directory %s: %w", dir, err)
	}

	var skills []skill
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("skill %s: symbolic links are not supported", entry.Name())
		}
		if !entry.IsDir() {
			continue
		}

		s, err := loadSkill(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", entry.Name(), err)
		}

		skills = append(skills, s)
	}

	return skills, nil
}

func loadSkill(dir, name string) (skill, error) {
	// Reads stay inside the skill directory even if a path is swapped for a symlink during the walk.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return skill{}, err
	}
	defer root.Close() //nolint:errcheck

	s := skill{name: name, files: make(map[string][]byte)}
	err = fs.WalkDir(root.FS(), ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}

		content, err := root.ReadFile(rel)
		if err != nil {
			return err
		}

		s.files[rel] = content
		return nil
	})
	if err != nil {
		return skill{}, err
	}

	content, ok := s.files[skillFile]
	if !ok {
		return skill{}, fmt.Errorf("missing %s", skillFile)
	}

	metadata, err := parseSkillMetadata(content)
	if err != nil {
		return skill{}, err
	}
	if err := validateSkillMetadata(metadata, name); err != nil {
		return skill{}, err
	}

	return s, nil
}

// parseSkillMetadata reads the YAML front matter that must open SKILL.md.
func parseSkillMetadata(content []byte) (skillMetadata, error) {
	text := strings.TrimPrefix(string(content), "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimRight(lines[0], " \t") != "---" {
		return skillMetadata{}, fmt.Errorf("%s must start with YAML front matter between --- lines", skillFile)
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") != "---" {
			continue
		}

		var metadata skillMetadata
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &metadata); err != nil {
			return skillMetadata{}, fmt.Errorf("invalid %s front matter: %w", skillFile, err)
		}
		return metadata, nil
	}

	return skillMetadata{}, fmt.Errorf("%s front matter is not closed with ---", skillFile)
}

func validateSkillMetadata(metadata skillMetadata, dirName string) error {
	switch {
	case len(metadata.Name) == 0:
		return fmt.Errorf("%s front matter must set name", skillFile)
	case len(metadata.Name) > maxSkillName || !skillNamePattern.MatchString(metadata.Name):
		return fmt.Errorf(
			"name %q must have at most %d lowercase letters, digits, and single hyphens between them",
			metadata.Name, maxSkillName,
		)
	case metadata.Name != dirName:
		return fmt.Errorf("name %q must match the directory name", metadata.Name)
	case len(strings.TrimSpace(metadata.Description)) == 0:
		return fmt.Errorf("%s front matter must set description", skillFile)
	case utf8.RuneCountInString(metadata.Description) > maxSkillDescription:
		return fmt.Errorf("description must have at most %d characters", maxSkillDescription)
	}

	return nil
}

// prepareSkillTarget writes the skills to one provider directory and removes files
// that an earlier generation wrote there but that no longer have a source.
func prepareSkillTarget(plan *fspatch.Plan, dir string, skills []skill, clean bool) error {
	manifestPath := filepath.Join(dir, skillManifestFile)
	previous, manifestExists, err := readSkillManifest(plan, manifestPath)
	if err != nil {
		return err
	}

	owned := make(map[string]bool)
	for _, file := range previous.Files {
		name, _, _ := strings.Cut(file, "/")
		owned[name] = true
	}

	var current []string
	for _, s := range skills {
		skillDir := filepath.Join(dir, s.name)
		if !owned[s.name] {
			if _, err := os.Lstat(skillDir); err == nil {
				return fmt.Errorf("skill %s exists and was not generated by go42x; rename the skill or remove %s",
					s.name, skillDir)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}

		for _, rel := range slices.Sorted(maps.Keys(s.files)) {
			filename := filepath.Join(skillDir, filepath.FromSlash(rel))
			if err := plan.Write(filename, s.files[rel], fspatch.KindSkills, clean); err != nil {
				return err
			}
			current = append(current, s.name+"/"+rel)
		}
	}

	for _, file := range previous.Files {
		if slices.Contains(current, file) {
			continue
		}

		filename := filepath.Join(dir, filepath.FromSlash(file))
		if _, exists, err := plan.Read(filename); err != nil {
			return err
		} else if exists {
			if err := plan.Remove(filename, fspatch.KindSkills); err != nil {
				return err
			}
		}
	}

	if len(current) == 0 {
		if manifestExists {
			return plan.Remove(manifestPath, fspatch.KindSkills)
		}
		return nil
	}

	content, err := json.MarshalIndent(skillManifest{Files: current}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", skillManifestFile, err)
	}

	return plan.Write(manifestPath, append(content, '\n'), fspatch.KindSkills, clean)
}

func readSkillManifest(plan *fspatch.Plan, filename string) (skillManifest, bool, error) {
	content, exists, err := plan.Read(filename)
	if err != nil || !exists {
		return skillManifest{}, exists, err
	}

	var manifest skillManifest
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return skillManifest{}, true, fmt.Errorf("failed to parse %s: %w", filename, err)
	}

	for _, file := range manifest.Files {
		// A manifest entry must name a file inside a skill directory of this target.
		if !strings.Contains(file, "/") || path.Clean(file) != file || !filepath.IsLocal(filepath.FromSlash(file)) {
			return skillManifest{}, true, fmt.Errorf("%s lists an invalid path %q", filename, file)
		}
	}

	return manifest, true, nil
}
