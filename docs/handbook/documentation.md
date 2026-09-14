---
id: documentation
title: Maintaining go42x documentation
collection: handbook
---

# Maintaining go42x documentation

## Purpose and ownership

Use this policy when writing or reviewing documentation for go42x commands, configuration, templates, and generated
outputs. It applies to work produced with AI assistance as well as other authored changes.

| Subject                                                                        | Owning source                                                                                                            |
|--------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------|
| Shared writing rules and the public evaluation and adoption journey            | The [go42 documentation](https://go42.dev/docs/documentation/)                                                           |
| Available commands, arguments, and configuration behavior                      | The [command definitions](../../internal/cmd/) and owning package sources in this repository                             |
| Default agent configuration and instruction templates                          | The [packaged templates](../../assets/agentenv/template/) and [generation implementation](../../pkg/agentenv/generator/) |
| Executable repository workflows and tool versions                              | [Taskfile.yaml](../../Taskfile.yaml), [mise configuration](../../etc/mise.toml), and its lockfile                        |
| An adopting application's configuration, handbook, requirements, and decisions | The application's own repository and documentation policy                                                                |

The agreed go42x role includes orchestration of project and documentation setup, shared defaults, validation, and
publishing. Describe capabilities according to the applicable implementation. The current
[command tree](../../internal/cmd/cmd.go) provides agent configuration, knowledge-base access, diagnostics, and MCP services.
Project setup and documentation publishing workflows remain planned work. Verify their implementation and availability
before teaching new commands or claiming platform support.

## Shared writing rules

The [public documentation policy](https://go42.dev/docs/documentation/) is the editorial home of these rules. This local
copy keeps them usable in the go42x checkout. Apply each rule to the page's purpose; choose useful headings and remove
empty or irrelevant template sections.

| Rule                                | Required practice                                                                                                                                                                                                     |
|-------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| State the scope                     | Identify the reader's task, promised result, prerequisites, and applicable software versions or environments.                                                                                                         |
| Make procedures executable          | Specify the working directory and required inputs. Separate copyable commands from output, preserve exact identifiers, and show expected results, effects on state, recovery, and cleanup where relevant.             |
| Support claims with evidence        | Link the relevant source or check. Distinguish inspected code, executed checks, proposals, and delivered behavior. Label assumptions and gaps; use disposable examples and keep credentials out of recorded evidence. |
| Make content accessible             | Use meaningful headings and links, readable examples, and text alternatives for informative images. Check navigation and the rendered meaning of changed content.                                                     |
| Maintain documentation with changes | Update the owning document with behavior changes, preserve important URLs and anchors, and explain documentation impact. Update authored inputs and regenerate derived output.                                        |
| Write consistently                  | Use direct language, stable terminology, and exact technical identifiers. Assume technical competence while explaining knowledge specific to go42.                                                                    |

Use **go42** for the upstream blueprint, **go42x** for its orchestration tool, **application** for a project adopting the
blueprint, and **local handbook** for that application's effective instructions. Preserve exact command names, flags,
configuration keys, output fields, and file paths.

## Documenting commands and generated state

State the go42x release or source revision that the example applies to, its working directory, prerequisite tools, and
relevant configuration. Explain how readers obtain placeholder values. Separate copyable commands, illustrative output,
and incomplete examples with language-tagged code fences and clear labels.

For a command, document required inputs, useful output, exit status, effects on files or other state, and relevant failure
and recovery behavior. Explain repeated execution where it can preserve, replace, or remove state. Keep credentials and
private configuration out of examples and retained output. Link to the implementation or test behind the behavior.

Aim for the same workflow across operating systems. Keep necessary installation differences explicit, and record the
platforms on which the procedure was executed. A packaged binary, successful compilation, and a completed workflow are
different kinds of evidence. State which was verified.

For generated content, identify the editable input and the command that produces the output. Explain which defaults a
project owns after adoption and how generation handles local edits. Change packaged defaults in the owning template or
generator; use the project's authored inputs when changing its local instructions. Record the relevant regeneration
results with the change.

## Maintaining policy and verification evidence

Update this policy and affected command explanations alongside changes to their behavior. Review public go42-docs
guidance when an advertised workflow changes, and review go42's local handbook when its effective instructions are
affected. Report unmapped documentation impact explicitly when source or retrieval links are incomplete.

Use the checks appropriate to the changed documentation and the behavior it describes. The
[Taskfile](../../Taskfile.yaml) defines the available repository checks. Record the command or method, relevant versions,
environment, expected result, observed result, and remaining gaps. Label source review, execution evidence, and planned
behavior separately. Review rendered content and navigation when a change affects publication.

When adopting a changed shared writing rule, update the local rule and policy edition together. Record the scope and
reason for local exceptions and coordinate changes with the corresponding go42 and go42-docs policies.

## Source files and authoring

Start at the [documentation index](../README.md). Maintain authored guidance in `docs/` and update an existing page when
it owns the subject. Handbook pages live in `docs/handbook/`; give a new page a lowercase, hyphenated filename, unique
`id`, descriptive `title`, and `collection: handbook` in YAML front matter. Include one visible H1 and add the page to the
index with a short purpose. These are authoring conventions; the current lint tasks do not validate the metadata schema.

Use relative links to local Markdown and source files, with `.md` on document links. Review inbound links and adjust
relative paths when moving a document. Preview Markdown in an editor or repository viewer and check headings, code
blocks, tables, and link destinations. go42x documentation currently has no website build or publishing task.

Edit the owning source template when changing generated instructions. Keep detailed task logs and temporary output in
`.build/`; retain durable facts in the owning handbook and evidence with the implementation review.

## Setup and documentation checks

Install mise and Task and make both available on `PATH`. The [mise configuration](../../etc/mise.toml) specifies the
minimum mise version and pinned project tools. From the go42x repository root, prepare the documentation tools:

```sh
task setup:docs
```

This installs locked Task, Node.js, Markdownlint, and Vale versions and downloads the configured style packages. Tools and
caches live in `.tools/`; downloaded styles live in `etc/.vale/styles/`. Initial setup needs network access. Run setup
again after changing the tool configuration, lockfile, or Vale packages. The full `task setup` also prepares these tools
alongside the other development dependencies.

After editing, run:

```sh
task docs:check
```

This checks Markdown and prose under `docs/` and exits with status zero on success. Use
`task lint:markdown -- docs/handbook/documentation.md` or `task lint:prose -- docs/handbook/documentation.md` for a targeted
check. Fix findings in the authored files and rerun the full documentation check before completing the change.

The [documentation lint job](../../.github/workflows/110-lint.yaml) runs `task setup:docs` and `task docs:check` in CI.
The [unified workflow](../../.github/workflows/100-unified-workflow.yaml) requires lint to pass before subsequent test and
build stages. Keep Task definitions, CI callers, and these instructions aligned when commands or check coverage change.

## Reviewing changes and retaining results

| Changed source                                    | Documentation to inspect                                                                                       |
|---------------------------------------------------|----------------------------------------------------------------------------------------------------------------|
| CLI commands or configuration                     | Local command guidance and examples; public go42-docs workflows that invoke the command.                       |
| Packaged templates or generators                  | Input/output ownership, rerun and recovery instructions, generated examples, and affected go42 handbook pages. |
| Tool versions, Taskfile, or release configuration | Installation, prerequisites, verification commands, and claims about platform coverage.                        |

Link related changes in go42 and go42-docs and identify the version their examples describe. Execute the affected
procedure when its commands, defaults, prerequisites, output, or state changes. Check repeated execution and recovery
where applicable. A wording edit needs proportionate Markdown review and documentation checks.

Keep the date, page and steps, documentation and go42x revisions, relevant go42 revision, local modifications,
OS/architecture, tool versions, initial state, commands, expected and actual results, cleanup, and remaining gaps with
the implementation change. Include the fields relevant to the procedure and link durable evidence from its owning
document. Update the example's applicability after rerunning it; retain earlier results with their original baseline.

Markdown and prose checks establish their configured rule results. Verify source links during review and run the command
or application checks needed to support changed instructions. Mark untested environments and planned capabilities
explicitly. When reporting a failure, include the page, step, revisions, environment, and expected and actual results,
with credentials removed from retained output.
