package generator

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go42-dev/go42x/internal/agentenv/generator/provider"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/antigravity"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/claude"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/codex"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/copilot"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/crush"
	"github.com/go42-dev/go42x/internal/agentenv/generator/provider/ghaw"
	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
)

func skillSource(name string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: Example skill for tests.\n---\n\n# %s\n", name, name)
}

func writeProjectFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func generateSkills(root, skillsDir string, providers ...string) error {
	cfg := &config.Config{
		Context:   config.Context{SkillsDir: skillsDir},
		Providers: make(map[string]config.Provider),
	}
	for _, name := range providers {
		cfg.Providers[name] = config.Provider{}
	}

	logger := slog.New(slog.DiscardHandler)
	g := NewGenerator(logger, cfg, filepath.Join(root, config.Directory), root)
	g.registerProviders()
	plan := fspatch.NewPlan(logger, root, filepath.Join(root, config.Directory, "backups"))
	if err := g.prepareSkills(plan, false); err != nil {
		return err
	}

	return plan.Apply()
}

func exists(t *testing.T, root, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

func TestPrepareSkillsTargetsEnabledProviders(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		providers []string
		want      []string
	}{
		{name: "claude", providers: []string{claude.Name}, want: []string{claude.SkillsDir}},
		{name: "codex", providers: []string{codex.Name}, want: []string{provider.SharedSkillsDir}},
		{name: "antigravity", providers: []string{antigravity.Name}, want: []string{provider.SharedSkillsDir}},
		{name: "copilot", providers: []string{copilot.Name}, want: []string{provider.SharedSkillsDir}},
		{
			name:      "claude and copilot",
			providers: []string{claude.Name, copilot.Name},
			want:      []string{claude.SkillsDir, provider.SharedSkillsDir},
		},
		{
			name:      "claude and codex",
			providers: []string{claude.Name, codex.Name},
			want:      []string{claude.SkillsDir, provider.SharedSkillsDir},
		},
		{
			name:      "gh-aw writes every directory",
			providers: []string{ghaw.Name},
			want:      []string{claude.SkillsDir, provider.SharedSkillsDir},
		},
		{
			name:      "gh-aw with claude",
			providers: []string{claude.Name, ghaw.Name},
			want:      []string{claude.SkillsDir, provider.SharedSkillsDir},
		},
		{name: "no skill support", providers: []string{crush.Name}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProjectFiles(t, root, map[string]string{
				".go42x/skills/review/SKILL.md":            skillSource("review"),
				".go42x/skills/review/references/guide.md": "Guide\n",
			})

			if err := generateSkills(root, "skills/", tt.providers...); err != nil {
				t.Fatal(err)
			}

			for _, dir := range []string{claude.SkillsDir, provider.SharedSkillsDir} {
				want := slices.Contains(tt.want, dir)
				for _, file := range []string{"review/SKILL.md", "review/references/guide.md", skillManifestFile} {
					if got := exists(t, root, dir+"/"+file); got != want {
						t.Errorf("%s/%s exists = %t, want %t", dir, file, got, want)
					}
				}
			}
		})
	}
}

func TestPrepareSkillsRemovesOnlyGeneratedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeProjectFiles(t, root, map[string]string{
		".go42x/skills/alpha/SKILL.md":   skillSource("alpha"),
		".go42x/skills/alpha/notes.md":   "Notes\n",
		".go42x/skills/beta/SKILL.md":    skillSource("beta"),
		".claude/skills/manual/SKILL.md": skillSource("manual"),
	})
	if err := generateSkills(root, "skills/", claude.Name); err != nil {
		t.Fatal(err)
	}
	writeProjectFiles(t, root, map[string]string{".claude/skills/alpha/user.md": "Added by hand\n"})

	if err := os.RemoveAll(filepath.Join(root, ".go42x/skills/beta")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".go42x/skills/alpha/notes.md")); err != nil {
		t.Fatal(err)
	}
	if err := generateSkills(root, "skills/", claude.Name); err != nil {
		t.Fatal(err)
	}

	for file, want := range map[string]bool{
		"alpha/SKILL.md":  true,
		"alpha/notes.md":  false,
		"alpha/user.md":   true,
		"beta/SKILL.md":   false,
		"manual/SKILL.md": true,
	} {
		if got := exists(t, root, ".claude/skills/"+file); got != want {
			t.Errorf("%s exists = %t, want %t", file, got, want)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, ".claude/skills", skillManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"files\": [\n    \"alpha/SKILL.md\"\n  ]\n}\n"; string(manifest) != want {
		t.Errorf("manifest = %q, want %q", manifest, want)
	}

	// Omitting the skill directory removes every generated file and the manifest.
	if err := generateSkills(root, "", claude.Name); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]bool{
		"alpha/SKILL.md":  false,
		"alpha/user.md":   true,
		"manual/SKILL.md": true,
		skillManifestFile: false,
	} {
		if got := exists(t, root, ".claude/skills/"+file); got != want {
			t.Errorf("after omitting skills-dir, %s exists = %t, want %t", file, got, want)
		}
	}
}

func TestPrepareSkillsKeepsCopiesOutOfGit(t *testing.T) {
	t.Parallel()
	block := "# go42x -- START\n/.gitignore\n/.go42x-skills.json\n/alpha/\n# go42x -- END\n"
	for _, tt := range []struct {
		name        string
		userRules   string
		wantWith    string
		wantWithout string // empty means the file is removed
	}{
		{name: "new file", wantWith: block},
		{name: "user rules", userRules: "user-rule\n", wantWith: "user-rule\n\n" + block, wantWithout: "user-rule\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{
				".go42x/skills/alpha/SKILL.md":   skillSource("alpha"),
				".agents/skills/manual/SKILL.md": skillSource("manual"),
			}
			if len(tt.userRules) > 0 {
				files[".agents/skills/.gitignore"] = tt.userRules
			}
			writeProjectFiles(t, root, files)
			gitIgnore := filepath.Join(root, provider.SharedSkillsDir, ".gitignore")

			if err := generateSkills(root, "skills/", codex.Name); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(gitIgnore); err != nil || string(got) != tt.wantWith {
				t.Fatalf(".gitignore = %q, %v; want %q", got, err, tt.wantWith)
			}

			if err := os.RemoveAll(filepath.Join(root, ".go42x/skills/alpha")); err != nil {
				t.Fatal(err)
			}
			if err := generateSkills(root, "skills/", codex.Name); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(gitIgnore)
			switch {
			case len(tt.wantWithout) == 0 && !os.IsNotExist(err):
				t.Fatalf(".gitignore = %q, %v; want it removed", got, err)
			case len(tt.wantWithout) > 0 && (err != nil || string(got) != tt.wantWithout):
				t.Fatalf(".gitignore = %q, %v; want %q", got, err, tt.wantWithout)
			}
			if !exists(t, root, provider.SharedSkillsDir+"/manual/SKILL.md") {
				t.Error("generation removed a skill added by hand")
			}
		})
	}
}

func TestPrepareSkillsPreservesDisabledProviderDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeProjectFiles(t, root, map[string]string{".go42x/skills/alpha/SKILL.md": skillSource("alpha")})
	if err := generateSkills(root, "skills/", claude.Name, codex.Name); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".go42x/skills/alpha")); err != nil {
		t.Fatal(err)
	}

	if err := generateSkills(root, "skills/", claude.Name); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, claude.SkillsDir+"/alpha/SKILL.md") {
		t.Error("enabled provider kept a skill without a source")
	}
	if !exists(t, root, provider.SharedSkillsDir+"/alpha/SKILL.md") {
		t.Error("generation changed the skill directory of a disabled provider")
	}
}

func TestPrepareSkillsRejectsSkillAddedByHand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manual := "---\nname: alpha\ndescription: Written by hand.\n---\n"
	writeProjectFiles(t, root, map[string]string{
		".go42x/skills/alpha/SKILL.md":  skillSource("alpha"),
		".claude/skills/alpha/SKILL.md": manual,
	})

	err := generateSkills(root, "skills/", claude.Name)
	if err == nil || !strings.Contains(err.Error(), "was not generated by go42x") {
		t.Fatalf("generateSkills() error = %v, want a collision error", err)
	}
	content, readErr := os.ReadFile(filepath.Join(root, ".claude/skills/alpha/SKILL.md"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != manual {
		t.Error("generation replaced a skill added by hand")
	}
}

func TestPrepareSkillsRejectsInvalidManifest(t *testing.T) {
	t.Parallel()
	for name, manifest := range map[string]string{
		"parent path":   `{"files": ["../AGENTS.md"]}`,
		"top level":     `{"files": ["SKILL.md"]}`,
		"unknown field": `{"files": [], "extra": true}`,
		"malformed":     `{"files": [`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProjectFiles(t, root, map[string]string{".claude/skills/" + skillManifestFile: manifest})

			if err := generateSkills(root, "skills/", claude.Name); err == nil {
				t.Fatal("generateSkills() accepted an invalid manifest")
			}
		})
	}
}

func TestLoadSkillsRejectsInvalidSkills(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		dir     string
		content string
		want    string
	}{
		{name: "missing skill file", dir: "alpha", want: "missing SKILL.md"},
		{name: "no front matter", dir: "alpha", content: "# Alpha\n", want: "must start with YAML front matter"},
		{name: "unclosed front matter", dir: "alpha", content: "---\nname: alpha\n", want: "is not closed"},
		{name: "invalid yaml", dir: "alpha", content: "---\nname: [\n---\n", want: "invalid SKILL.md front matter"},
		{name: "missing name", dir: "alpha", content: "---\ndescription: Example.\n---\n", want: "must set name"},
		{name: "name mismatch", dir: "alpha", content: skillSource("beta"), want: "must match the directory name"},
		{name: "uppercase name", dir: "Alpha", content: skillSource("Alpha"), want: "lowercase letters"},
		{name: "double hyphen", dir: "a--b", content: skillSource("a--b"), want: "single hyphens"},
		{name: "trailing hyphen", dir: "alpha-", content: skillSource("alpha-"), want: "single hyphens"},
		{
			name:    "long name",
			dir:     strings.Repeat("a", maxSkillName+1),
			content: skillSource(strings.Repeat("a", maxSkillName+1)),
			want:    "at most 64",
		},
		{name: "missing description", dir: "alpha", content: "---\nname: alpha\n---\n", want: "must set description"},
		{
			name:    "long description",
			dir:     "alpha",
			content: "---\nname: alpha\ndescription: " + strings.Repeat("x", maxSkillDescription+1) + "\n---\n",
			want:    "at most 1024 characters",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{tt.dir + "/README.md": "Readme\n"}
			if len(tt.content) > 0 {
				files[tt.dir+"/SKILL.md"] = tt.content
			}
			writeProjectFiles(t, root, files)

			_, err := loadSkills(root, ".")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("loadSkills() error = %v, want %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "skill "+tt.dir+":") {
				t.Errorf("loadSkills() error = %v, want the skill name", err)
			}
		})
	}
}

func TestLoadSkillsReadsSkillDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeProjectFiles(t, root, map[string]string{
		"skills/README.md":            "Index of skills\n",
		"skills/.git/config":          "[core]\n",
		"skills/alpha/SKILL.md":       "\ufeff---\r\nname: alpha\r\ndescription: Windows line endings.\r\n---\r\nBody\r\n",
		"skills/alpha/.DS_Store":      "junk",
		"skills/alpha/.cache/x":       "junk",
		"skills/alpha/scripts/run.sh": "#!/bin/sh\n",
	})

	skills, err := loadSkills(root, "skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].name != "alpha" {
		t.Fatalf("loadSkills() = %+v, want only alpha", skills)
	}
	files := make([]string, 0, len(skills[0].files))
	for file := range skills[0].files {
		files = append(files, file)
	}
	slices.Sort(files)
	if want := []string{"SKILL.md", "scripts/run.sh"}; !slices.Equal(files, want) {
		t.Errorf("skill files = %v, want %v", files, want)
	}

	missing, err := loadSkills(root, "absent")
	if err != nil || len(missing) != 0 {
		t.Errorf("loadSkills(absent) = %v, %v; want no skills", missing, err)
	}
}
