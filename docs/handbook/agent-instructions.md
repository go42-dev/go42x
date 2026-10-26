---
id: agent-instructions
title: Agent instructions
collection: handbook
sidebar_position: 5
---

# Agent instructions

Generate project instructions with `task x -- agentenv generate`. Git remote
metadata excludes HTTP and HTTPS usernames and passwords before rendering.
Malformed HTTP remotes are omitted. SSH remotes retain their Git username.

## Generation commands

Run `task setup:generators` to install the pinned generation tools, then use
`task generate` to refresh all routine generated project files.

| Command                 | Outputs                                                    |
| ----------------------- | ---------------------------------------------------------- |
| `task generate`         | Runs `generate:code`, then `generate:agentic`              |
| `task generate:code`    | Go mocks and Go module metadata                            |
| `task generate:agentic` | Shared instructions, gh-aw MCP definitions, workflow locks |

`generate:agentic` uses the checked-out source and shared configuration with
`--no-local`. CI runs the code and agentic tasks in separate jobs. For local
provider settings, use `task x -- agentenv generate`. The preparation action
also calls this CLI command directly to generate instructions for its run.

## Generation and secret scans

Generation checks the complete rendered instructions before writing files.
Size warnings report the consumer, configuration source, instruction file, byte
count, limit, and limit source. They do not fail generation or shorten the
instructions. Missing imports, invalid settings, and unresolved consumers fail
generation.

After generation, run `task lint:secrets -- AGENTS.md` to scan the generated
instructions with Gitleaks. Explicit files are scanned directly, including
ignored and untracked files. Findings are redacted; secret findings, unreadable
files, and scanner errors fail the task. Without file arguments, `lint:secrets`
scans Git history. MCP configuration files containing authentication are outside
the explicit `AGENTS.md` scan.

The preparation action runs generation and the secret scan before building the
knowledge base and starting the agent. CI also scans the instructions after
generating agentic workflows.

## Consumers and budgets

Enabled providers select local instruction consumers. Workflow Markdown selects
workflow engines, including engines from local imports. A workflow without an
engine uses gh-aw's Copilot default. Unknown engines fail rather than bypassing
the check. Shared instructions without a selected consumer use the project budget.

| Consumer                                | Checked budget                                      | Source                                       |
| --------------------------------------- | --------------------------------------------------- | -------------------------------------------- |
| Codex                                   | 32,768 bytes, overridden by `project_doc_max_bytes` | Runtime configuration                        |
| Claude                                  | 4,194,304 bytes per startup file                    | Runtime limit                                |
| Antigravity                             | 24,000 bytes per file after expanding includes      | Runtime limit                                |
| Copilot CLI, Crush, shared instructions | 24,000 bytes                                        | Project policy; native limits are unverified |

Configure the local Codex budget under `providers.codex` in `.go42x/go42x.yaml`
or its local override:

```yaml
providers:
  codex:
    project-doc-max-bytes: 65536
```

Generation writes this value as `project_doc_max_bytes` in `.codex/config.toml`
and uses it for the instruction size check in the same run. The value must be
a non-negative integer; zero is an explicit zero-byte budget. Omitting it
preserves the existing Codex setting. Without an explicit value, local checks
read user configuration, then project configuration, falling back to 32,768 bytes.
The bundled configuration selects 65,536 bytes.

Workflow checks read `engine.config` independently of local Codex settings. The
workflow sources explicitly set `project_doc_max_bytes = 65536`.

Checks cover generated shared instructions and their supported local imports.
They include the generated header, count UTF-8 bytes, and ignore import examples
inside fenced code. Imported files are checked separately; Antigravity includes
and Copilot imports also contribute to the containing file's measured size.
The preparation action and CI scan the generated shared file. Include imported
files in `task lint:secrets -- FILE ...` if they contain generated data.

Personal instructions, instructions loaded later for particular paths, runtime
configuration overrides passed on the command line, and aggregate token budgets
are outside this check. Antigravity's separate 20,000-token budget cannot be
certified by counting project-file bytes.

For runtime behavior, see [Codex instruction loading](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[Claude instruction loading](https://code.claude.com/docs/en/memory), and
[Antigravity rules](https://www.antigravity.google/docs/rules/).

Run project-tool queries through Task, for example
`task tool -- mise current go:github.com/go42-dev/go42x`, so mise receives the
project configuration location and tool directories.
