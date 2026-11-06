---
name: go42x-skills
description: Create, change, or share agent skills in a project managed by go42x, where skill sources live in .go42x/skills and generation copies them for each agent. Use when asked to add or edit a skill, to reuse skills from another repository, or when a skill does not appear in an agent.
---

# Agent skills with go42x

A skill is a directory with a `SKILL.md` file and optional supporting files. Agents see only each skill's name
and description until a task matches; then they load the rest.

In a go42x project, skill sources live in `.go42x/skills/<name>/`, set by `context.skills-dir` in
`.go42x/go42x.yaml`. `go42x agentenv generate` copies every skill to the directories that enabled agents read:

| Agent              | Directory                                                             |
| ------------------ | --------------------------------------------------------------------- |
| Claude Code        | `.claude/skills/`                                                     |
| Codex, Antigravity | `.agents/skills/`                                                     |
| Copilot            | `.claude/skills/` when Claude is enabled, otherwise `.agents/skills/` |

Edit the sources, not the copies. Generation replaces the copies it wrote, removes copies whose source is gone,
and never touches skills added to those directories by hand. Each directory keeps a `.go42x-skills.json` list of
the files that generation wrote and a `.gitignore` block that hides the copies from Git, so only skills added by
hand show up as changes.

## Write a skill

1. Create `.go42x/skills/<name>/SKILL.md`. The directory name must equal the skill name: lowercase letters,
   digits, and single hyphens, at most 64 characters.
2. Start the file with front matter:

   ```markdown
   ---
   name: release-notes
   description: Draft release notes from merged changes. Use when asked for release notes or a changelog entry.
   ---
   ```

   The description is all an agent reads before choosing the skill. Say what the skill does and when to use it,
   in words people will type, within 1024 characters.
3. Write the instructions below the front matter. Keep `SKILL.md` short and move long references or scripts into
   subdirectories that `SKILL.md` links to. Copies are not executable, so call scripts through their interpreter,
   for example `sh scripts/check.sh`.
4. Run `go42x agentenv generate`. Hidden files and directories are not copied.

## Share skills between projects

Keep shared skills in their own repository and vendor them into `.go42x/skills/`. Use the project-root
`.vendor.yaml`; go42x replaces `.go42x/.vendor.yaml` on update. List each file of a skill:

```yaml
version: "1.0"
dependencies:
  team-skills:
    source:
      type: git
      url: https://example.com/team/agent-skills.git
      ref: main
    files:
      - from: release-notes/SKILL.md
        to: .go42x/skills/release-notes/SKILL.md
```

Then run `go42x vendor update` and `go42x agentenv generate`. Vendored files are overwritten on update, so change
them in the shared repository.
