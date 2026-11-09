package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentEnvSkills(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	p.configure(t)
	if !strings.Contains(p.read(t, ".go42x/skills/go42x-skills/SKILL.tpl.md"), "name: go42x-skills\n") {
		t.Fatal("init did not install the bundled example skill")
	}
	if strings.Contains(p.read(t, ".gitignore"), ".agents/skills") {
		t.Error("the project .gitignore hides skills that users commit to .agents/skills")
	}

	config := strings.Replace(p.read(t, ".go42x/go42x.yaml"),
		"  template: agents.tpl.md\n", "  template: agents.tpl.md\n  skills-dir: skills/\n", 1)
	if !strings.Contains(config, "skills-dir") {
		t.Fatal("test configuration has no context template to extend")
	}
	p.write(t, ".go42x/go42x.yaml", config)
	p.write(t, ".go42x/skills/review/SKILL.md", "---\nname: review\ndescription: Review a change.\n---\n")
	p.write(t, ".go42x/skills/review/references/checklist.md", "Checklist {{ .project.name }}\n")
	p.write(t, ".go42x/skills/notes/SKILL.tpl.md",
		"---\nname: notes\ndescription: Notes for {{ .project.name }}.\n---\n{{ .skills.dir }}\n")
	p.write(t, ".claude/skills/manual/SKILL.md", "Written by hand\n")
	p.run(t, 0, "agentenv", "generate")

	// The fixture enables Claude, plus Codex, Antigravity, and Copilot, which read the shared directory.
	targets := []string{".claude/skills", ".agents/skills"}
	for _, dir := range targets {
		for _, file := range []string{"review/SKILL.md", "review/references/checklist.md"} {
			if p.read(t, dir+"/"+file) != p.read(t, ".go42x/skills/"+file) {
				t.Errorf("%s/%s does not match its source", dir, file)
			}
		}
		if got := p.read(
			t,
			dir+"/notes/SKILL.md",
		); got != "---\nname: notes\ndescription: Notes for example-e2e.\n---\nskills\n" {
			t.Errorf("%s/notes/SKILL.md was not rendered: %q", dir, got)
		}
		bundled := p.read(t, dir+"/go42x-skills/SKILL.md")
		for _, want := range []string{
			"skill sources live in `.go42x/skills/`",
			"| `.claude/skills/` | claude |",
			"| `.agents/skills/` | antigravity, codex, copilot |",
		} {
			if !strings.Contains(bundled, want) {
				t.Errorf("%s/go42x-skills/SKILL.md does not contain %q", dir, want)
			}
		}
		if !strings.Contains(p.read(t, dir+"/.gitignore"), "\n/go42x-skills/\n/notes/\n/review/\n") {
			t.Errorf("%s/.gitignore does not list the generated skills", dir)
		}
	}

	if err := os.RemoveAll(filepath.Join(p.root, ".go42x/skills/review")); err != nil {
		t.Fatal(err)
	}
	p.run(t, 0, "agentenv", "generate")
	for _, dir := range targets {
		if _, err := os.Stat(filepath.Join(p.root, dir, "review/SKILL.md")); !os.IsNotExist(err) {
			t.Errorf("generation kept %s/review without a source: %v", dir, err)
		}
	}
	if p.read(t, ".claude/skills/manual/SKILL.md") != "Written by hand\n" {
		t.Error("generation changed a skill added by hand")
	}

	// An invalid skill stops generation before any output changes.
	p.write(t, ".go42x/agents.tpl.md", "# Changed {{ .project.name }}\n")
	p.write(t, ".go42x/skills/broken/SKILL.md", "# No front matter\n")
	before := p.snapshot(t)
	result := p.run(t, 1, "agentenv", "generate")
	if !strings.Contains(result.stderr, "skill broken") {
		t.Errorf("generation error does not name the invalid skill: %q", result.stderr)
	}
	p.assertUnchanged(t, before)
}
