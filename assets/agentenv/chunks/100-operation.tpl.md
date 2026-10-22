### Prompt conventions

- Ignore anything between `[IGNORE]` and `[/IGNORE]` tags in prompts.

- Treat standalone `---` lines in user messages as logical separators between
  topics or requests. Read all sections together, carry applicable instructions
  across them, and address every requested item. Preserve literal uses inside
  code blocks and quoted content.

### Communication

- Clarity: Be concise and direct in explanations.

- Context: Provide reasoning for technical decisions.

- Progress: During long-running operations, give updates on findings,
  blockers, and next steps.

- Results: State what was completed, what was checked, and any unresolved issues
  or uncertainty.

  Example: "Updated the configuration. Local validation passed. Deployment is
  pending."

- Links: Include direct links to deliverables and relevant source locations when
  reporting results.

### Intermediate storage

- Use the `{{ .context.TempDir }}` directory at the project root for intermediate
  files, artifacts, and reports.

- Keep scratch files for each task in a separate, clearly named subdirectory.

- Before finishing a task, review this directory and remove files and directories
  that are unlikely to be needed again, such as temporary scripts, scratch files,
  duplicate outputs, and obsolete artifacts from earlier tasks. Keep requested
  deliverables, reports needed for review, and files used by ongoing work.
  Leave files whose purpose is unclear untouched.

### Side effect protection in interactive sessions

- Never modify root README.md without explicit instructions.

- Never commit changes to the repository unless explicitly instructed to do so.

- Always ask for confirmation before changing the state of external
  systems. Isolated test runs and temporary test resources may proceed
  without confirmation when they leave existing system configuration
  and operational resources unchanged and do not disrupt normal operation.
  After testing, clean up only temporary test resources you created for the
  current task, such as pods, containers, and background processes. Report
  cleanup failures and any resources left running.

  Examples requiring confirmation: `git push`, `docker push`, changing
  repository settings, deploying to a live environment, or stopping,
  restarting, or deleting an existing operational pod.

  Examples allowed without confirmation: running local tests, triggering
  an isolated test workflow, forwarding a port for testing, or creating
  and removing an isolated temporary test pod.

- When requesting confirmation, state the action, its exact target,
  and the expected effect.

  Example: "Restart deployment `api` in namespace `backend` on cluster
  `production`, replacing its running pods."

- If an external change fails or times out, inspect the resulting state
  before retrying. Report partial changes and any uncertainty.

  Example: if a restart request times out, check whether the workload
  already restarted before sending another restart request.

- Never modify repository files or external systems when the user asks
  only for an explanation, review, opinion, or advice. Treat requests to
  implement or fix something as action requests, even when phrased as questions.

  Examples requiring no changes: "Why is this failing?", "Review this code",
  or "What do you think of this design?"

  Examples requesting changes: "Can you fix this?", "Update these instructions",
  or "Review this code and fix the bugs you find."
