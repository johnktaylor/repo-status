# How repo-status works: a code walkthrough

This is a tour of how `repo-status` is put together, for readers who want to trust the tool, fix
it, or extend it. It is written in plain language and explains the technical words it needs.

This handbook is for human readers. It is not the authority on the program and it is not a
knowledge source for automated assistants. The Go source and tests are the authority; `README.md`
and `repos.yaml.template` describe the contract. If this tour and the code disagree, the code
wins and this tour should be corrected.

## The pieces

Everything lives in the repository root:

- `main.go` holds the command dispatch, the four commands, the Git status call, and the usage
  text.
- `config.go` holds the configuration types and the loading and validation routine.
- `capture.go` holds the bounded output capture: per-target byte limits, the buffers that keep
  only a prefix, and the messages that report truncation.
- `render.go` holds the rendered-report ceiling and the small helpers that stream a JSON array one
  record at a time and escape messages written to standard error.
- `output.go` holds three small helpers: making text safe for a terminal, refusing an existing
  or stream-shaped output path, and publishing a rendered report file without overwriting.
- `workers.go` holds the fixed-size worker pool used by `--async`.
- `config_test.go`, `main_test.go`, `capture_test.go`, and `render_test.go` hold the tests. There
  is no separate code library to learn: these few source files are the program.
- `repos.yaml.template` is the starting point users copy to `repos.yaml`.
- `README.md` is the command reference; this handbook is the explanation.

The program is small on purpose: a handful of source files, one dependency (`gopkg.in/yaml.v3`,
which reads YAML), and only the Go standard library otherwise.

## A run from start to finish

1. `main` looks at the arguments. With no arguments it prints the usage summary and exits with
   status 1.
2. It reads the first argument. `list`, `path`, and `exec` pick a subcommand; anything else is
   treated as the configuration file for the default status view.
3. The chosen handler reads the configuration file. Every handler does this before doing any
   work, so an invalid file never produces partial output or a half-written report.
4. The handler asks Git or starts the requested command, prints results, and returns an exit code.
5. `main` exits with that code when it is not zero.

Exit status is the number a program hands back to the shell: `0` for success, `1` for a failure
the program detected, and `2` for an option the parser could not understand. That last one comes
from Go's option parser, which exits before a handler runs; the handlers themselves only return
`0` or `1`.

## Reading the configuration file

`loadAndValidateConfig` in `config.go` turns the file into a `Config` value: a list of `Repo`
values, each with a `Name` and a `Location`. The steps are:

1. Reject an alternate data stream path on Windows, then turn the given path into an absolute
   path. Require a regular file: a directory, device, or pipe is refused before opening, and the
   opened file is checked again in case the path changed. A file larger than 1 MiB
   (1,048,576 bytes) is rejected, and reading stops one byte beyond that limit, so a file that
   grows during the read still cannot be consumed in full.
2. Read and decode it as YAML with unknown fields rejected. YAML is the indentation-based text
   format used for the file; rejecting unknown fields means a misspelled key becomes an error
   instead of a silently ignored setting.
3. Decode one more time. If that second read does not hit the end of the file, a second YAML
   document is present, which is rejected. Without this check the extra document would be
   ignored.
4. Reject a file that defines no repositories at all, and one that defines more than 1,000. An
   empty or comments-only file ends before any document is read, and a null document such as a
   lone `---` decodes to nothing; both cases are reported as "contains no repositories" instead
   of as a cryptic YAML message.
5. For each entry: trim surrounding whitespace from the name and the location, reject a blank
   name, reject a comma, a control character, or a Unicode bidirectional formatting character in
   the name, reject a duplicate name (compared in lower case, so `api` and `API` collide), reject
   a blank location, reject Windows alternate streams and partial paths, and resolve a relative
   location against the folder that contains the file.

The name rules are not cosmetic. A comma separates selectors, so a name containing one could
never be selected reliably; control and bidirectional formatting characters can be invisible or
reorder text, so they cannot be shown or typed safely. `escapeDisplay` in `output.go` also escapes
those characters wherever a name, location, diagnostic, or error is printed.

`validateWindowsLocation` runs only on Windows and rejects alternate data stream syntax plus the
three shapes that would otherwise be joined to the configuration folder and silently point
somewhere else:

- `C:repos` is drive-relative, so it depends on the current folder of that drive;
- `\repos` is rooted on whatever drive happens to be current;
- `\\server` names a host but no share, and Go would otherwise treat it as absolute;
- `C:\repo:stream` addresses a named stream rather than a folder and is refused.

Complete absolute paths and complete share paths pass. Device paths such as `\\?\C:\folder` are
accepted without inspection because they follow their own rules.

Two checks are deliberately left out: the folder is not checked for existence, and Git is not run
here. Loading stays fast that way, and a wrong path surfaces as a Git error in that repository's
block, where the message can name the real problem.

## Picking repositories

`findRepo` accepts a bare selector and returns the matching configured entry itself, not a copy,
so callers see the trimmed, resolved values. It first trims the selector, then:

- if it is a whole number between 1 and the number of repositories, that position wins;
- otherwise it is compared to names exactly, including letter case, with no special treatment for
  labels that begin with `name:`;
- if it was a number but matched no name, the error says the index is out of range, so `5` on a
  two-repository file does not look like a typo in a name;
- any other miss reports `repository not found`.

`findRepoByName` is the opt-in exact form behind `path --name` and `exec --names`. It trims the
label, rejects an empty one, and compares names only, with no numeric-index interpretation. That is
how a numeric label or a label that begins with `name:` is selected without ambiguity.

`resolveTargetRepos` handles `exec --repos`: split on commas, trim each piece, reject an empty
piece, resolve each selector, and remember what was already added so an overlapping selection runs
once. Resolution happens before the first command starts, so a mistake stops the run instead of
doing part of it.

`resolveNamedRepos` does the same for `exec --names`, but calls `findRepoByName`, so every entry is
a label and none is read as an index. `runExec` checks flag presence with the option parser:
`--repos` and `--names` together are rejected before the configuration is even loaded, and an
empty value for either is a usage error. Omitting both means every configured repository.

## The status view

`runStatus` is the default command. After loading the configuration, it resolves the `-o`
destination, refuses it if anything already exists there, and only then queries repositories. It
calls `fetchRepoStatusDetailsLimited` with the run's per-target capture limit, which runs one Git
process per folder:

```
git --no-optional-locks -c core.fsmonitor=false status --porcelain
```

`--no-optional-locks` stops Git from taking lock files it would only take to refresh caches, and
`core.fsmonitor=false` stops it from invoking a configured file-system monitor. Git 2.36 or newer
reads that value as a boolean; per Git's `git-config` documentation, Git 2.35.1 and earlier treat
it as a hook path. Neither option isolates the query; they reduce incidental writes to the
repository. The command still reads the machine's, the user's, and the repository's Git settings
and the surrounding environment.

Each query runs with a 30-second deadline and a 5-second wait for output pipes to close after the
process stops (`gitStatusTimeout` and `gitStatusWaitDelay` in `main.go`), so one stuck repository
cannot stall the whole run. `exec` deliberately has no deadline.

Porcelain is Git's compact status format designed for programs: empty when the working tree is
clean, otherwise a list of two-character codes. Using it means one process answers two questions:
what to display, and whether the repository is dirty. No second Git call is needed, and the text
shown in JSON mode is the same text shown in text mode.

The helper captures the process's normal output and its error output in separate bounded streams
that share the target's byte allowance. Only normal output becomes status text. Error output
becomes diagnostics, trimmed of surrounding whitespace. That separation matters because Git may
write a warning to its error stream while still succeeding: mixing it into the status would make a
clean repository look dirty, and dropping it would hide the warning.

If the process fails, the returned error is `checking status: <failure>` and the diagnostics are
returned separately. That is why text output shows a short `Error:` line followed by a distinct
`Git diagnostics:` line, and why JSON has separate `error` and `diagnostics` properties. A
timeout produces `checking status timed out after <duration>: context deadline exceeded`, with a
truncation note appended if output was also cut off. If the capture limit was reached, the error
names the stream, for example `Git output truncated after 1048576 bytes (stdout)`, even when Git
exited successfully; the retained prefix is still returned and displayed.

The text view colors the heading by state: green for clean, yellow for dirty, red for failure. The
colors are ordinary terminal escape codes. They are only written when the report is going to a
real terminal rather than a file or a pipe, which `isTerminal` decides by asking the operating
system whether the output is a character device, and never when `-o` is in use. Redirected output
therefore stays plain.

`--dirty` drops repositories that are clean and readable, but keeps ones that failed, so a problem
cannot vanish by asking for a shorter report. `--json` builds a small record per repository with
`name` and `status`, plus `diagnostics` and `error` when present.

Rendering happens after every query finishes. `runStatus` keeps one record per repository, then
asks `renderStatusJSON` or `renderStatusText` to write them. JSON is streamed as one array with one
record at a time, so the report does not have to exist as one large encoded value. Text status
keeps the porcelain lines exactly as separate rows, escaping each row for display, and prints any
captured status text before the `Error:` line when a query failed or was truncated.

Rendered output has two 128 MiB caps in `render.go`. `maxRenderedBytesPerRun` applies to
`list --json`, status output (text and JSON), `exec` JSON, and `-o` reports.
`maxExecTextBytesPerRun` applies to the streamed `exec` text path, including dry-run lines. Plain
`list` text and both `path` modes have no rendered cap. JSON gets a preflight render into a
discarding writer before anything is written to real output, so an over-limit JSON report fails
without leaving half an array behind. Text is written block by block, so earlier complete blocks
may already be on screen when the cap trips; the run then reports the error and exits with status
1. With `-o`, the render happens into a temporary file, so a failed or over-budget render is
removed rather than published.

For the file form this also means no output file exists while Git is running: a report written
inside a repository cannot appear as one of its own changes.

## Writing the report without overwriting

`-o` goes through two helpers in `output.go`, and both first reject Windows alternate data stream
syntax:

- `ensureNewOutputPath` asks for the destination's metadata without following a link. Anything
  that exists is refused: a file, a folder, a symbolic link, a hard link, or the configuration
  file itself.
- `publishReportFile` renders the report through `publishRenderedFile`: it creates a temporary
  file in the destination's folder using a fixed-shape hidden name that does not reuse the
  destination name, renders the report into it under the 128 MiB cap, flushes and closes it, and
  then creates a hard link from the temporary name to the requested name. Linking is atomic and
  refuses an existing name, unlike a rename, which replaces the destination on some platforms.
  The temporary name is removed whether or not publishing worked.

The preflight check gives a clear early error and avoids doing any Git work when the destination
is obviously taken. The hard link closes the gap between that check and the final write: if the
destination appears while Git is running, the link still fails. Both failures report "output file
already exists; refusing to overwrite".

This design has two requirements worth knowing. The destination's folder must exist and be
writable, and its filesystem must support hard links. A filesystem that does not reports a
publish error instead of silently replacing anything. The destination folder is assumed to be
trusted, because the temporary file is created there.

## `list` and `path`

`runList` prints position, name, and location per repository, or the same information as JSON with
explicit `index`, `name`, and `location` fields. The index is simply the entry's position in the
list plus one, which is what users type as a selector. Text output escapes each displayed value
with `escapeDisplay`; JSON output relies on JSON's own escaping.

`runPath` resolves one selector, either bare (with `findRepo`) or with `--name` (with
`findRepoByName`). Text mode writes the stored path exactly as-is when the output is not a
terminal, with no trailing newline, so command substitution receives the real bytes. Only terminal
output is escaped for display. JSON mode prints an object with a single `path` property.

`escapeDisplay` turns control, bidirectional, line-separator, and paragraph-separator characters
into visible text such as `\x1b` or `\u2028` and leaves ordinary characters, including Windows
backslashes, alone. A repository name may contain a line or paragraph separator, because those are
not control characters; it is accepted and only escaped when displayed. Status rows are escaped
line by line so they keep their real rows, while free-form messages written to a terminal or to
standard error are kept on one line with `\x0a` standing in for a line break. JSON values and the
chosen command's output are not changed.

Neither command runs Git. Both check every write to their output stream and report a failure with
status 1 instead of claiming success.

## exec

`runExec` parses its own options, loads the configuration, resolves the target repositories, and
then chooses the text or JSON renderer. `--repos` and `--names` are checked before the
configuration is loaded: using both is rejected, and either one with an empty value is a usage
error. A bare `--repos` entry is resolved with `findRepo`; a `--names` entry is resolved with
`findRepoByName`, so numeric labels and labels beginning with `name:` work there.

Each target runs the command directly, with the first word as the program and the remaining words
as its arguments, and with the repository folder as the working directory. No shell is involved,
so shell syntax is not expanded. That is a deliberate safety choice as much as a simplicity one:
nothing is interpreted twice. The command runs with the same user, environment, and network access
as `repo-status` itself. There is no sandbox.

Each renderer captures the command's normal output and error messages through `capture.go`'s
bounded buffer, which shares one byte allowance between both streams, and records whether it
failed. A non-zero exit marks the run as failed but does not stop the other repositories. Names,
locations, and error text are escaped for display, but the command's own output is passed through
unchanged, because the user chose that command and may want its exact bytes.

There is no timeout: the command is allowed to finish. That differs from the status view, which
has a fixed per-repository deadline.

Dry-run mode is handled before any concurrency: it walks the targets in selection order, records
one line each, and starts nothing.

- Without `--async`, repositories run one after another in selection order, and the text blocks
  appear in that order.
- With `--async`, `runBoundedConcurrently` starts at most 8 workers, or one per repository when
  there are fewer than 8. It hands out indexes over a channel and waits for every worker, so the
  limit holds no matter how many repositories are selected. Text blocks are assembled per
  repository and printed under a lock (a mutex), so two commands cannot interleave mid-line, but
  the order follows completion.
- JSON results are written into a list by position, so the array stays in selection order no
  matter which command finishes first.

In text mode the first output failure is remembered under the same lock and later writes are
skipped; at the end the renderer reports the error and returns 1. JSON output is streamed one
record at a time through `render.go`; because an encoded record is written as a unit, a write
failure cannot leave half a record behind, and a preflight pass catches an over-budget report
before anything reaches standard output. The failure flag is an atomic value, meaning it is safe
to update from several workers at once.

## Captures and limits

`capture.go` keeps child output from exhausting memory:

- `captureLimitForTargets` returns `min(1 MiB, 64 MiB / target count)`, never less than one byte.
  Status passes the configured repository count; `exec` passes the number of selected targets, so
  every target in a run gets the same allowance and the run's retained output stays bounded.
- `boundedBuffer` keeps the first bytes of a command's combined output. `boundedStreams` keeps
  status stdout and stderr separately so diagnostics never mix into status text, but the two
  share one byte budget.
- Both writers always report the full write to the operating system, so a child process never
  blocks because its capture filled up. The discarded remainder is only recorded.
- Truncation is an error, not a silent trim. Status reports `Git output truncated after N bytes`
  with the stream names; `exec` joins `command output truncated after N bytes` to any command
  error with `; `. Either way the run exits with status 1, and the retained prefix is what appears
  in the text or JSON output.

`render.go` adds the rendered-output caps: `maxRenderedBytesPerRun` (128 MiB) for `list --json`,
status output (text and JSON), `exec` JSON, and `-o` files, and `maxExecTextBytesPerRun` (128 MiB)
for the streamed `exec` text path, including dry-run lines. Plain `list` text and `path` do not use
either cap. A write that would cross its limit fails before any partial chunk is emitted.

## Tests

The four test files exercise behavior without building the binary, calling the handlers directly
with temporary configuration files:

- `config_test.go` covers validation: blank name and location, unknown and misspelled fields,
  duplicate names including case variants, multiple documents, empty and comments-only files, the
  1 MiB limit at exactly the boundary and one byte over, the 1,000-repository limit at exactly the
  boundary and one over, names containing commas or control characters, Windows partial paths, and
  Windows alternate data streams for the configuration file and locations. It also covers selector
  lookup and dirty detection against a real `git init` repository.
- `main_test.go` installs a fake Git by copying the test binary onto `PATH` under the name `git`
  and selecting canned responses from directory names. That makes it possible to prove that
  status runs one Git process per repository, that warnings stay out of the status text, that
  `--dirty` keeps failures, that `-o` never overwrites a destination created before or during the
  run, that an invalid configuration leaves an existing report untouched, that a report written
  inside a repository does not include itself, that `--name`, `--names`, and bare selectors behave,
  that every command reports output failures, and that `--async` never runs more than eight
  commands at once while keeping JSON results in selection order.
- `capture_test.go` covers the per-target limit arithmetic, the shared stdout/stderr budget, prefix
  retention, exec and Git truncation reporting, the short-deadline timeout path, and the literal
  name behavior of bare selectors versus `--name` and `--names`.
- `render_test.go` covers display escaping of line and paragraph separators, one-line prefixed
  errors, and the rendered limit refusing a chunk that does not fit.

The real-Git tests need Git on the test machine; the fake-Git tests do not. `fetchRepoStatus` and
`isRepoDirty` are thin wrappers used by the tests; the status view calls
`fetchRepoStatusDetailsLimited` directly so it can display warnings separately. Run the suite from
the repository root:

```bash
go test ./...
```

## Where to make a change

- A new subcommand: add a case in `main`, write the handler, add it to `printUsage`, and update
  `README.md` and the user guide.
- A new option: add it to that subcommand's option set, document it in `README.md` and the user
  guide, and describe any new exit behavior.
- A new configuration field: add it to the type in `config.go`, validate it there, document it in
  `README.md` and `repos.yaml.template`, and add tests, including the boundary cases.
- Different output: update the renderer, then check every sample in `README.md` and the handbooks,
  because the samples are meant to match real output rather than illustrate it loosely.

There are no retries and no remote configuration. If one of those is needed, it is a new feature
rather than a switch that is already hidden somewhere.
