---
id: cli-codestyle
title: CLI Code Style
collection: handbook
sidebar_position: 2
related:
  - cli-output
---

# CLI Code Style

Use these rules when adding or changing a command. Keep command configuration,
feature behavior, and result formatting with their owners. Follow
[CLI output](cli-output.md) for streams, result formats, and exit status.

## Command files and ownership

| Location                                    | Responsibility                                                    |
| ------------------------------------------- | ----------------------------------------------------------------- |
| `internal/cmd/<feature>/cmd.go`             | Command group, shared flags, and subcommand registration          |
| `internal/cmd/<feature>/<operation>.go`     | Command definition, typed settings, and its execution helper      |
| `internal/<feature>/settings.go`            | Feature settings, defaults, validation, and settings resolution   |
| `internal/<feature>/service.go`             | Feature operations and their result types                         |
| `internal/<feature>/adapters/cli/report.go` | Text rendering and translation of results into CLI reports        |
| `internal/cmdutil/`                         | Shared CLI infrastructure, streams, reporting, and error handling |

- Keep a command constructor and its matching execution helper in the same file.
  For example, `newGenerateCommand` and `runGenerateCommand` belong in `generate.go`.
- Keep helpers with the code that owns their behavior. Extract shared CLI policy
  into `cmdutil` when several commands need it.
- Group related declarations in an existing file when they belong together.
  Small result types can live beside their service; a helper or type does not
  require its own file.

## Command help

Describe what a command accomplishes and what the user needs to know to use it.

- Use `Short` for a single line describing the command's purpose.
- Use `Long` for context that helps someone use the command, such as its scope,
  significant defaults, or behavior that might surprise them. Omit it when it
  adds nothing useful beyond `Short`.
- Put invocation examples in `Example` and explain individual options in their
  flag descriptions.
- Keep detailed behavior, routine file maintenance, backup mechanics, and
  implementation details in the handbook. Include them in help only when they
  affect a user's decision about running the command.

For example, describe indexing and its incremental behavior without listing the
files maintained during a build:

```go
cmd := &cobra.Command{
    Use:   "build",
    Short: "Build or update the project knowledge-base index",
    Long: `Index project documentation and source files for knowledge-base search.
Only changed files are reindexed. Use --rebuild to rebuild the entire index.`,
}
```

## Typed settings and validation

Construct feature settings explicitly in `RunE`. Keep feature flags, including
`JSON`, in those settings and pass the same value to the execution helper and
reporter. Shared CLI options, such as the log level, belong to the factory's
options.

Validate command inputs before initializing dependencies or running an operation.
Return `cmdutil.UsageError(err)` for invalid command settings. Services may also
validate their inputs for other callers; that does not replace CLI validation.

The following reduced example assumes the command group registers `--json` and
the root command binds the selected command's flags to Viper:

```go
const (
    cleanFlag = "clean"
    jsonFlag  = "json"
)

func newGenerateCommand(f *cmdutil.Factory) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "generate",
        Short: "Generate agent configuration",
        Args:  cobra.NoArgs,
        RunE: func(_ *cobra.Command, _ []string) error {
            settings := &agentenv.Settings{
                Clean: viper.GetBool(cleanFlag),
                JSON:  viper.GetBool(jsonFlag),
            }

            if err := settings.Validate(); err != nil {
                return cmdutil.UsageError(err)
            }

            return runGenerateCommand(f, settings)
        },
    }

    cmd.Flags().Bool(cleanFlag, false, "regenerate owned instruction files")
    return cmd
}
```

Where a feature supplies `NewSettings`, use it for defaults that do not come from
flags. Use `cmdutil.ExplicitFlag` when an explicit empty or false flag value must
override environment or project configuration. A value alone does not establish
whether the user supplied a flag.

If project configuration changes the settings, load it, validate the resulting
settings, and resolve paths before constructing the service. Keep this preparation
in the command's execution helper and report failures through its CLI adapter.

## Service execution and results

- Keep Cobra and Viper reads in the command layer. Services receive typed settings
  and dependencies through constructors and options.
- Pass the factory's context to service operations so cancellation reaches the
  work they start.
- Services return feature results and errors. CLI adapters own presentation and
  the interpretation of presentation settings such as `JSON`.
- Pass both the result and error to the adapter, including partial results that
  contain recovery information. Report initialization failures through the same
  adapter so the selected format also applies to those failures.

The execution helper connects those responsibilities:

```go
func runGenerateCommand(f *cmdutil.Factory, settings *agentenv.Settings) error {
    service, err := agentenv.NewAgentEnvService(
        settings,
        agentenv.WithLogger(slog.Default()),
    )
    if err != nil {
        return cli.ReportGenerate(
            f.Output(),
            settings,
            nil,
            fmt.Errorf("failed to initialize agentenv service: %w", err),
        )
    }

    result, err := service.Generate(f.Context())
    return cli.ReportGenerate(f.Output(), settings, result, err)
}
```

See the complete [command implementation](../../internal/cmd/agentenv/generate.go),
including provider selection.

## CLI adapters and shared reporting

A CLI adapter receives a writer, typed settings, a result, and an operation error.
It selects the text renderer and passes reporting to `cmdutil.Report`. The shared
reporter owns JSON encoding, output errors, and already-reported failures.

Return every write error from the text renderer. Successful feature work does not
make a failed report successful.

```go
func ReportGenerate(
    output io.Writer,
    settings *agentenv.Settings,
    result *agentenv.GenerateResult,
    runErr error,
) error {
    var text func(io.Writer) error
    if !settings.JSON {
        text = func(w io.Writer) error {
            if runErr != nil {
                return nil
            }

            _, err := fmt.Fprintf(w,
                "Generated agentenv; %d file changes applied.\n", len(result.Changes))
            return err
        }
    }

    return cmdutil.Report(output, result, runErr, text)
}
```

This renderer prints a success summary. Other renderers may print partial results
or recovery information on failure. When a diagnostic report already describes
the failure, the adapter marks the error with `cmdutil.ReportedError` so Cobra
does not print it again. A failure absent from that report must still be reported.

Keep feature rendering in its [CLI adapter](../../internal/agentenv/adapters/cli/report.go)
and shared reporting behavior in [cmdutil](../../internal/cmdutil/result.go).

## Streams and logging

- Use `f.Input()`, `f.Output()`, and `f.ErrorOutput()` in execution helpers.
  Reporters accept writers so callers can capture or redirect output.
- Write results through the CLI adapter. Use `slog` for progress and diagnostics,
  and inject the configured logger into services through options.
- Configure logging and bind command flags in the root command's setup. Feature
  services and reporters use the dependencies supplied to them.

The [factory](../../internal/cmdutil/factory.go) supplies streams and context;
the [root command](../../internal/cmd/cmd.go) connects them to Cobra and configures
logging. The [output policy](cli-output.md) defines their public behavior.

## Cleanup and error ownership

The execution helper closes resources it acquires and includes cleanup failures
in the reported error. Combine operation and cleanup errors without losing the
operation's result:

```go
func runBuildCommand(f *cmdutil.Factory, settings *kwb.Settings) error {
    service, err := kwb.NewService(settings, kwb.WithLogger(slog.Default()))
    if err != nil {
        return kwbcli.ReportBuild(f.Output(), settings, nil, err)
    }

    result, err := service.BuildIndex(f.Context(), settings.RootPath)
    err = errors.Join(err, service.Close())
    return kwbcli.ReportBuild(f.Output(), settings, &result, err)
}
```

Here, `kwbcli` names the `internal/kwb/adapters/cli` import. If cleanup fails after
a diagnostic operation, its existing report may not describe that failure. Pass
the cleanup failure through the adapter's ordinary runtime-error path.

Return errors from execution helpers and reporters. Process exit belongs to the
entry point. The root command uses `cmdutil.ConfigureCommands` after registering
subcommands to classify argument and flag errors and suppress duplicate reports.

See the [build runner](../../internal/cmd/kwb/build.go) and the
[diagnostic runner](../../internal/cmd/kwb/check.go) for these two reporting cases.

## Verification

Choose tests that exercise the affected boundary:

- Service tests cover feature behavior and partial results.
- Reporter tests cover text and JSON output, write failures, and error ownership.
- CLI tests cover validation before side effects, selected formats, redirected
  streams, cancellation where relevant, and exit status.
- Verify that changing the log level does not change or hide a command result.

Use the [shared reporter tests](../../internal/cmdutil/result_test.go) and
[CLI reporting tests](../../tests/e2e/reporting_test.go) as examples. Run the
project's relevant checks and report what they verified.
