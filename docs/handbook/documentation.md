---
id: documentation
title: Writing go42x documentation
collection: handbook
---

# Writing go42x documentation

Write docs that help someone use or change go42x. Start at the [documentation index](../README.md) and update the page
that covers the command, setting, or template you are changing.

The go42 guide explains shared ideas. Application teams maintain their own instructions. Keep the details of go42x
commands and generated files in this repository.

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

## Writing command examples

Say what the command does, where to run it, and what the reader needs first. Explain where placeholder values come from.
Use the exact names and flags from the command's help and implementation.

Show useful output and explain how to tell whether the command worked. Explain exit codes that scripts need to handle.
Say which files or other data it creates, changes, or deletes, and what happens if someone runs it again. Explain common
failures and how to recover, including how to undo changes when needed. Keep commands and output in separate code blocks
with language labels.

Try the example before calling it ready. If the steps differ across operating systems, explain the differences and say
where you tried them. Mention version differences only when they change what the reader needs to do. Label features
that are still planned.

## Explaining generated files

Tell readers which files they edit and which command generates the output. Explain what happens to their local edits
when they run that command again.

Change shared defaults in the [packaged templates](../../assets/agentenv/template/) or
[generator](../../pkg/agentenv/generator/). Change a project's instructions in that project's source templates. Run the
generator and inspect its output after changing a template.

## Adding a page

Keep handbook pages in `docs/handbook/`. Use a lowercase filename with hyphens between words. Give each page a YAML
header with a unique `id`, a clear `title`, and `collection: handbook`, followed by one H1 heading matching the title.
Keep the ID when renaming or moving the page.

Add the page to the [index](../README.md) with a short description. Use relative links to local files, including `.md`
for Markdown pages. Fix links when a page moves. Read the page in an editor or repository viewer and check its headings,
examples, tables, and links.

## Checking changes

Install mise and Task first. From the go42x repository root, prepare the documentation tools:

```sh
task setup:docs
```

The full `task setup` also installs them. Repeat setup when the tool configuration or Vale styles change.

After editing, run:

```sh
task docs:check
```

This runs Markdownlint and Vale on `docs/`. It checks formatting and prose; it does not validate page headers or build a
website. Check links and try any changed command examples yourself. CI runs the same documentation checks.

In the change description, say what you checked, what happened, and anything still untested. Include environment details
when they explain the result. Keep temporary files in `.build/` and useful instructions in the handbook.

## Keeping the guides consistent

Update docs with the commands and templates they describe. Check the go42 handbook and go42 guide when the change also
affects their examples. When a shared writing rule changes, update it in all three projects and explain any local
difference.
