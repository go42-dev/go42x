---
id: documentation
title: Writing project documentation
collection: handbook
sidebar_position: 1
---

# Writing project documentation

Write docs that help someone use, change, or troubleshoot go42x. Start at the
[documentation index](../README.md) and update the page that covers your subject.

## Documentation ownership

Project contributors maintain the docs for this checkout, including its commands, settings, changes, and procedures.

### Bundled agent material

go42x ships agent material from `assets/agentenv/` and installs it under `.go42x/` in consuming projects.
Choose its location by how it is maintained and used:

| Directory    | Content                                                     | How agents use it                  |
| ------------ | ----------------------------------------------------------- | ---------------------------------- |
| `chunks/`    | Instruction templates maintained by go42x                   | Included in generated instructions |
| `resources/` | Documents, examples, and supporting files authored in go42x | Read when needed for a task        |
| `upstream/`  | External documents tracked by the vendoring manifest        | Read when needed for a task        |

Keep chunks concise. Link to resources for detailed rules, explanations, and examples, and state when agents must
read them. Resources are copied as files; their contents are not rendered into generated instructions.
For example, `resources/go-codestyle.md` contains the Go defaults and examples used by the style chunk.

Maintain shared resources in `assets/agentenv/resources/` and keep this checkout's `.go42x/resources/` copy in sync.
Use ordinary Markdown with descriptive filenames and headings for documents. Resources support topics beyond
code style and do not need entries in the vendoring manifest. The knowledge-base index includes supported resource
files unless project exclusions omit them.

`agentenv init` installs missing bundled files and preserves existing files. `agentenv export` includes resources
in the exported bundle. `agentenv update --base` merges resource customizations with a previously exported bundle;
conflicts leave project files unchanged and save candidates for review. Without `--base`, update replaces bundled
resources. Both update modes preserve additional project files. Vendored upstream files follow their replacement
policy in both modes.

## Shared writing rules

Use these rules when writing or reviewing docs, including work done with AI assistance.

- Start with the reader's task. Say what they will learn or do and what they need first.
- Use plain language. Keep sentences short and explain unfamiliar terms.
- Make examples easy to use. Say where to run commands, explain placeholders, and show the expected result.
  Keep commands and output in separate code blocks. Use exact command names, flags, and paths, with sample values
  instead of secrets.
- Check your instructions. Try the steps you describe. Say what you have not tested and label planned features.
- Make pages easy to scan. Use clear headings, useful link text, and descriptions for images.
- Update docs with the code. Fix affected instructions and links in the same change.

## Change workflow for contributors

Update docs in the same change as the behavior they describe:

- Change the handbook when commands, settings, or procedures change.
- Describe expected behavior and explain important choices on the page that covers the subject.
- Label proposals and planned capabilities clearly. Record what works, what was checked, and what is still missing.

Read the relevant handbook pages before starting. Compare instructions with the code and tests. If they disagree,
find out which needs fixing. Keep useful reasoning when guidance changes, while making the current instructions clear.

Check related guidance in go42 and bundled agent instructions when the same change affects their readers.
In the change description, say which docs changed, what you checked, and anything still untested.
Keep detailed logs with the change.

## Creating a document

Use an existing page when it covers the topic. For a new page:

1. Create a Markdown file in `docs/handbook/` with a descriptive lowercase filename, such as `example-topic.md`.
2. Add the page metadata and one H1 heading that matches its title.
3. Write the explanation or steps. Choose useful headings and remove sections you do not need.
4. Add the page to the [documentation index](../README.md).
5. Run the [checks](#verification).

### Metadata and filenames

The YAML header at the top of a page is called front matter. Every authored page needs `id`, `title`, and `collection`.
Use a unique ID that starts with a letter and contains only letters, numbers, and hyphens. Keep it when a page is renamed
or moved, and never reuse an old ID for a different subject.

Use `collection: handbook` for handbook pages and `collection: overview` for the index.
Give each handbook page an unused positive integer for `sidebar_position`. An optional `related` list contains IDs
of other documents that exist. Filenames do not need a number prefix.

For example, a new handbook page could start with:

```markdown
---
id: example-topic
title: Example topic
collection: handbook
sidebar_position: 4
---

# Example topic
```

Replace the example values and choose a position that is unused when adding the page.

### Maintaining the index

List every handbook page once under **Handbook** in the [documentation index](../README.md).
Each table row has two cells: one link to the document and a short description.

Put general guidance before pages for specific tasks, and keep related pages together.
Update the index when a page is added, moved, renamed, removed, reordered, or given a different purpose.

## Verification

Install the pinned documentation tools from the repository root if needed:

```sh
task setup:docs
```

Then run:

```sh
task docs:check
```

This runs Markdown lint and Vale prose checks on authored Markdown under `docs/`. Fix any failures and rerun the checks.
Vale uses pinned Google and write-good rules at the error level, with spelling checks disabled.
It does not validate metadata, index coverage, links, or command behavior; review those separately.

Tables must use leading and trailing pipes, aligned columns, the same number of cells in each row, and blank lines
around the table. Use your editor's table formatter or align the pipes manually.

Read the changed pages in a Markdown preview. Check headings, examples, and links. Confirm that document IDs are unique,
related IDs exist, handbook positions are unique, and the index includes the changed pages in a useful reading order.

When you change instructions, try the affected steps from the starting point you describe. Markdown lint cannot tell
whether a command works. In the review, say what you ran, what happened, and what you could not test.
Include environment details when they affect the result. Keep secrets out of examples and logs.
