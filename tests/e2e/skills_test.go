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
	if !strings.Contains(p.read(t, ".go42x/skills/go42x-skills/SKILL.md"), "name: go42x-skills\n") {
		t.Fatal("init did not install the bundled example skill")
	}
	if !strings.Contains(p.read(t, ".gitignore"), "\n.agents/skills/\n") {
		t.Error("init did not ignore generated shared skills")
	}

	config := strings.Replace(p.read(t, ".go42x/go42x.yaml"),
		"  template: agents.tpl.md\n", "  template: agents.tpl.md\n  skills-dir: skills/\n", 1)
	if !strings.Contains(config, "skills-dir") {
		t.Fatal("test configuration has no context template to extend")
	}
	p.write(t, ".go42x/go42x.yaml", config)
	p.write(t, ".go42x/skills/review/SKILL.md", "---\nname: review\ndescription: Review a change.\n---\n")
	p.write(t, ".go42x/skills/review/references/checklist.md", "Checklist\n")
	p.write(t, ".claude/skills/manual/SKILL.md", "Written by hand\n")
	p.run(t, 0, "agentenv", "generate")

	// The fixture enables Claude, plus Codex and Antigravity, which read the shared directory.
	targets := []string{".claude/skills", ".agents/skills"}
	for _, dir := range targets {
		for _, file := range []string{"go42x-skills/SKILL.md", "review/SKILL.md", "review/references/checklist.md"} {
			if p.read(t, dir+"/"+file) != p.read(t, ".go42x/skills/"+file) {
				t.Errorf("%s/%s does not match its source", dir, file)
			}
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
