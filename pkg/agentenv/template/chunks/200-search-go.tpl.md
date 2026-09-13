{{ if or (hasMCPTool .mcp "go42x" "kwb_search") (hasMCPTool .mcp "go42x" "kwb_get_file") (hasMCPTool .mcp "go42x" "kwb_list_files") (hasMCPTool .mcp "go42x" "kwb_stats") -}}
### Searching documentation and code

Use the knowledge-base tools from the `go42x` MCP server to find documentation sections, code declarations,
configuration keys, examples, and related files. Use an available Go language server for references,
implementations, type information, and call relationships. Use text search for literal
or regular-expression searches, especially when checking recently edited files.

The knowledge base indexes Markdown by heading and Go by declaration. Other text
formats use bounded line chunks. Exact symbols, headings, and filenames receive
more weight than body matches. Code identifiers can also match component words:
`NewHTTPServer` is searchable as `http server`.

| Tool                         | Arguments                                                              | Result                                                                               |
|------------------------------|------------------------------------------------------------------------|--------------------------------------------------------------------------------------|
{{ if hasMCPTool .mcp "go42x" "kwb_search" -}}
| `kwb_search`     | `query`; optional `kind`, `language`, `path_prefix`, `limit`, `offset` | Ranked snippets with path, title, kind, language, score, and source line ranges      |
{{ end -}}
{{ if hasMCPTool .mcp "go42x" "kwb_get_file" -}}
| `kwb_get_file`   | `path`; optional `start_line`, `end_line`                              | Current source lines and the next line to read                                       |
{{ end -}}
{{ if hasMCPTool .mcp "go42x" "kwb_list_files" -}}
| `kwb_list_files` | Optional `type`, `language`, `path_prefix`, `limit`, `offset`          | Unique files in path order, total, and the next offset                               |
{{ end -}}
{{ if hasMCPTool .mcp "go42x" "kwb_stats" -}}
| `kwb_stats`      | None                                                                   | File count (`document_count`), chunk count, project root, index path, and generation |
{{ end }}

These tools return structured JSON with a matching JSON text fallback.

Search queries are plain text. Use `kind="documentation"` for prose,
`kind="code"` for source, or `kind="config"` for configuration. The file listing
uses the argument `type` for the same categories. `language` narrows further,
for example `go`, `md`, `ts`, or `json`. `path_prefix` is a project-relative prefix.

{{ if and (hasMCPTool .mcp "go42x" "kwb_search") (hasMCPTool .mcp "go42x" "kwb_get_file") -}}
Example workflow:

1. Search with `query="http server", kind="code", language="go"`.
2. Read a result using its `path`, `start_line`, and `end_line`.
3. Use an available Go language server to resolve references or implementations.
4. Follow `next_offset` to paginate or `next_start_line` to continue reading.
{{ end }}

Search defaults to 10 results and allows at most 100 per page. Search offsets
are limited to 10,000; `window_limited=true` means filters are needed to reach
additional matches. File listing defaults
to 100 files and allows up to 500 per page, without the former 1,000-file cutoff.
If the returned `generation` changes between pages, restart pagination to avoid
mixing snapshots. Search totals count chunks; file-list totals count unique files.

File reads default to 200 lines and are limited to 500 lines and 64 KiB per
response. Line numbers are one-based and inclusive. Paths resolve relative to
the indexed project root, including when the server starts elsewhere. File reads
reflect current source; search results reflect the last completed index update.

Run `go42x kwb` after changing files. It hashes eligible source files, indexes
only changed files, and removes deleted or newly ignored files. An unchanged
project does not publish another index. Use `go42x kwb --rebuild` for a full rebuild.
The old index remains available until the new generation is published, and a
running MCP server picks it up on its next request.

Indexing respects `.gitignore` files inside the selected root, including nested
rules and negation. It excludes its own index directory, build/tool directories,
binary files, symlinks, generated Go files, and common lockfiles. Use
`--include-ext=.xyz` to add an extension, or a filename such as `Makefile.custom`.
The index is local and requires no embedding service.

Set `go42x mcp --search-timeout=5s` or `GO42X_SEARCH_TIMEOUT=5s` to control the
maximum duration of knowledge-base reads. Flags override `GO42X_*` environment
variables. Build settings include `--root`, `--index`, `--exclude-dir`,
`--include-ext`, and `--rebuild` (`GO42X_REBUILD=true`).
{{ end }}
