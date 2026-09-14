---
id: knowledge-base
title: Local knowledge base
collection: handbook
sidebar_position: 2
---

# Local knowledge base

Use the local knowledge base to retrieve project source and documentation through the go42x MCP server. These instructions
apply to the source changes for KB-01 through KB-06, dated September 14, 2026. They require a binary built from this
checkout; the changes have no assigned release yet. No embedding service or external RAG service is required.

## Build and check

From a project root, run these commands with that binary on `PATH`:

```sh
go42x kwb build
go42x kwb check --json
```

Build hashes eligible files, updates changed chunks, and removes deleted or newly excluded files. An unchanged source
tree and unchanged build settings preserve the generation. A changed selection policy or a legacy manifest without
recorded settings publishes updated metadata. Use `go42x kwb build --rebuild` for a full rebuild.

`check` reads the index and hashes eligible source using the last build's selection flags and the current ignore files.
It does not update or create an index. This scan costs a filesystem walk and content reads; ordinary searches and MCP
context requests do not perform it. The result describes files observed during the scan, not an atomic source snapshot.

| Result | Meaning |
| --- | --- |
| `status: fresh`, `complete: true` | Eligible source matched the reported generation during the scan. |
| `status: stale` | The scan found new, changed, deleted, or newly excluded files. |
| `status: missing` or `unavailable` | The index could not be opened. Review diagnostics and rebuild if needed. |
| `status: incomplete` | The scan could not establish freshness. Review diagnostics; it may need more time or a build. |
| `truncated: true` | Path samples or settings were shortened to bound output; counts still describe the completed scan. |

Exit status is 0 only for a complete, fresh check; it is 1 otherwise. Invalid command usage exits 2. `new`, `changed`,
`deleted`, and `excluded` contain counts and bounded path samples. `excluded` means a previously indexed file still
exists but no longer meets eligibility rules, including ignore rules, generated content, size, and file type. Files
that were never indexed are not enumerated as exclusions. The output includes the generation and recorded selection
settings. Counts from an incomplete scan are partial.

The default read timeout is five seconds. For a larger project, run `go42x kwb check --search-timeout=30s --json`.
An index change during the scan requires a retry. A legacy index without recorded selection settings requires a build
before freshness can be established. `go42x doctor` also checks freshness and reports stale or incomplete scans.

See the [freshness implementation](../../pkg/kwb/freshness.go), [CLI](../../internal/cmd/kwb/check.go), and
[regression tests](../../pkg/kwb/freshness_test.go).

## Control source coverage

Indexing respects nested `.gitignore` rules. It excludes symlinks, binary content, generated Go files, common lockfiles,
build directories, and index storage. Authored `.go42x/go42x.yaml`, top-level `*.tpl.md`, templates under
`.go42x/chunks/`, `.go42x/kwb.ignore`, and `.env.example` are eligible. Local overrides, backups, generated schemas, and
other `.go42x` state are excluded. Git ignore rules still apply to authored files.

`kwb build` creates `.go42x/kwb.ignore` when it is missing, containing this header and a blank line:

```gitignore
# go42x knowledgebase will ignore the following files and directories
```

Existing files are preserved, including empty files and custom headers. Add Git ignore patterns relative to the project
root below the header. For example:

```gitignore
# go42x knowledgebase will ignore the following files and directories

/static/swagger/swagger-ui-bundle.js
/static/swagger/swagger-ui-standalone-preset.js
```

This keeps handwritten JavaScript searchable. Negation can override earlier patterns in this file, but cannot override
Git ignores or built-in exclusions. Excluded directories are pruned, so an exception inside one requires allowing its
parent directory. The ignore file must be a regular file and is limited to 64 KiB.

Build flags include `--exclude-file`, `--exclude-dir`, `--include-ext`, and `--max-file-size`. For example:

```sh
go42x kwb build --exclude-file='*.min.js' --include-ext=.custom
```

Repeat custom flags on later builds, or put project file exclusions in `.go42x/kwb.ignore`. `check` reuses the recorded
flags automatically. A changed ignore file is evaluated immediately by `check`; build to apply the change to retrieval.
See the [shared file selection rules](../../pkg/kwb/files.go).

## Retrieve context and project guidance

Supply exact identifiers and affected paths to `project_context`, for example task `Explain Service.Init and
Service.Refresh` with paths `pkg/agentenv/service.go`. Existing supplied files are read from current source even when
absent from the index. Directories scope source searches by path boundary, with global searches supplying supporting
evidence. Deleted paths remain useful for documentation impact.

Context selects distinct declarations or documentation sections from the same file. It preserves section titles,
document IDs, statuses, and replacement chains. Exact symbol matches take priority over general keyword matches;
indexed candidates are combined by query rank and verified against current source hashes. Review `reason`, `query`,
coverage diagnostics, and source continuation pointers. A stale indexed source is omitted; stale document matches are
reselected from live documentation.

Configure project guidance using authored document IDs. For a project with IDs `project` and `conventions`, start:

```sh
go42x mcp --context-doc=project,conventions
```

Alternatively set `GO42X_CONTEXT_DOC="project conventions"` in the MCP server environment. Flags take precedence. Add
the flag to the server's arguments in the application's authored `.go42x/go42x.yaml`, then run `go42x agentenv generate`
and restart the client. Up to eight IDs are supported; unavailable IDs produce a diagnostic.

The documentation entrypoint and configured guidance share at most one quarter of the source budget when other
evidence is available. Unused space returns to requested evidence. The default source budget is 16 KiB, the maximum is
48 KiB, and encoded context is bounded to 64 KiB. Truncated source provides `next_start_line` when content was returned.
Use `kwb_get_file` or `docs_get` to continue. Selection diagnostics also report omitted paths, searches, and ranges.

`kwb_search` keeps snippet coordinates and adds `chunk_id`, `symbols`, `chunk_start_line`, and `chunk_end_line` for the
original chunk. A chunk can be one part of a long declaration. See the [context implementation](../../pkg/kwb/context.go)
and [focused regression tests](../../pkg/kwb/context_test.go).

## Prepare an agent session

Generate agent files before refreshing retrieval:

```sh
go42x agentenv generate
go42x kwb build
go42x kwb check --json
```

Run these commands from the adopting project root. Repeat any custom build flags. Generation updates client output;
build refreshes eligible source. Ignored generated outputs remain excluded. The go42 AI preparation action runs its
incremental build after generation. Its tool version must include these changes to use the new coverage controls.

## Verification scope

The focused unit tests cover source selection, exact methods, distinct sections, historical metadata, context bounds,
freshness classification, custom build settings, missing indexes, root checks, and deadlines. The
[command tests](../../tests/e2e/kwb_check_test.go) exercise freshness output and exit status, and the
[MCP tests](../../tests/e2e/mcp_test.go) exercise guidance configuration through stdio. Local execution is on
darwin/arm64; other platforms require their CI checks. This change does not add the broader retrieval evaluation suite
or alter documentation entrypoint and legacy CI MCP setup.
