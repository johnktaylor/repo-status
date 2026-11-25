# protoworks-repo-status

`repo-status` keeps a set of local Git repositories visible from one place. From a single YAML
configuration file it shows the Git status of every repository, lists them, prints one
repository's folder, and runs the same command inside several repositories.

## Requirements

- Git on your `PATH` for the default status view, version 2.36 or later recommended. The status
  query passes `-c core.fsmonitor=false`; Git 2.35.1 and earlier read `false` as the name of an
  fsmonitor hook to run rather than as a boolean, according to Git's `git-config` documentation,
  so those versions can misbehave. `repo-status` does not check the installed version at run time.
- `exec` needs whatever program you name, which is Git only when your command is a Git command.
  `list` and `path` only read the configuration file and run nothing.
- Go 1.25.1 or later to build from source.

## Build and test

```bash
go build .
go test ./...
```

`go build .` writes the `repo-status` binary to the current folder (`repo-status.exe` on
Windows). You can also run from source with `go run . <arguments>`. The examples below use the
binary name.

## Configuration

Copy `repos.yaml.template` to `repos.yaml` and replace the example values:

```yaml
repositories:
  - name: my-repository
    location: C:\path\to\repository
```

`.gitignore` excludes local YAML files, because a configuration file contains paths that only
make sense on one machine.

### Fields

- `repositories`: the list of repositories to manage. Add one entry per repository.
  - `name` *(string, required)*: a label you choose. It appears in output and selects the
    repository in `path` and `exec --repos`.
  - `location` *(string, required)*: the repository's folder. A relative path is resolved
    against the folder containing the configuration file, and every location is treated as an
    absolute path afterwards.

### Validation

The configuration is checked at the start of every run, before any other work happens. A file
is rejected when:

- the path does not name a regular file; a directory, device, or pipe is refused instead of being
  read, and the opened file is checked again in case the path changed;
- it contains more than one YAML document, meaning a second document after a `---` line;
- it contains an unknown or misspelled key; only `repositories` is allowed at the top level and
  only `name` and `location` inside an entry;
- it defines no repositories at all, which includes an empty file, a comments-only file, and
  `repositories: []`;
- it defines more than 1,000 repositories;
- it is larger than 1 MiB (1,048,576 bytes). Reading is capped at one byte beyond that limit, so
  an oversized file cannot be consumed even if it grows while it is being read;
- a `name` or `location` is blank (surrounding whitespace is trimmed from both first);
- two names match ignoring letter case, so `api` and `API` conflict;
- a name contains a comma, because commas separate selectors, contains a control character such
  as a tab, newline, or escape, or contains a Unicode bidirectional formatting character that
  could reorder how the name is displayed.

On Windows, a path that addresses an alternate data stream, such as `repos.yaml:stream`, is
rejected for the configuration file, for every repository location, and for the `-o` destination.
The drive-letter colon is not affected.

Locations are not checked when the file is loaded: a folder that is missing, or that is not a
Git repository, is reported later by Git for that repository. Only local folders are supported;
remote URLs are not.

On Windows, locations that name only part of a path are rejected rather than silently joined to
the configuration folder:

- `C:repos` is drive-relative and rejected; use `C:\path\to\repos`;
- `\repos` is drive-rooted and rejected; include a drive letter or use a relative path;
- `\\server` is an incomplete UNC path and rejected; `\\server\share\folder` is accepted;
- device paths such as `\\?\C:\folder` are used as given.

## Selectors

A bare selector, as used by `path` and `exec --repos`, is a one-based position or an exact
repository name:

- Surrounding whitespace is ignored.
- A whole number selects a position when it is between 1 and the number of repositories. If it
  is out of range, it is matched against names, so a repository named `42` remains reachable. If
  neither matches, the error is `index out of range`.
- Any other value must match a name exactly, including letter case.
- A bare selector has no special prefixes: `name:` is just part of a name, so a repository
  literally called `name:api` is selected with `name:api`.

Two options force exact-name selection, which is how you reach numeric labels or labels that
begin with `name:`:

- `path --name <label> <config_file>`: the selector is always an exact name, never an index.
- `exec --names <labels> <config_file> <command>`: a comma-separated list of exact names.

```bash
repo-status path --name 42 repos.yaml
repo-status path --name name:legacy repos.yaml
repo-status exec --names api,web repos.yaml git status
```

`--names` and `--repos` are mutually exclusive and either one requires a value. Every name or
selector is resolved before anything runs, so one unknown or empty entry cancels the whole run;
a repository selected twice runs only once. `--repos` takes a comma-separated list of bare
selectors; an empty piece such as `1,,2`, or a value of only spaces, is an error. Omitting both
flags selects every configured repository.

## Commands

```
repo-status [--dirty] [-o <new_output_file>] [--json] <config_file>
repo-status list [--json] <config_file>
repo-status path [--name] [--json] <selector> <config_file>
repo-status exec [--repos <selectors> | --names <labels>] [--async] [--dry-run] [--json] <config_file> <command> [command_arguments...]
```

Options go before the positional arguments. Each option may be written with one or two leading
dashes (`--dirty` or `-dirty`). The first argument that is not an option ends option parsing,
which is what lets `exec` pass a command's own options through unchanged.

### Default: Git status

```bash
repo-status repos.yaml
repo-status --dirty repos.yaml
repo-status --json -o report.json repos.yaml
```

- `--dirty`: show only repositories with uncommitted or untracked changes. A repository whose
  status cannot be read is always shown.
- `-o <output_file>`: write the report to a new file instead of the screen. Existing paths are
  never overwritten; see "Writing the report to a file" below.
- `--json`: emit a JSON array of repository-status objects instead of text.
- `--short`: accepted for compatibility and has no effect, because output always uses Git's
  porcelain (short) format.

Each repository is checked with one Git process:

```
git --no-optional-locks -c core.fsmonitor=false status --porcelain
```

The query has a 30-second deadline. When the deadline passes, the process is stopped and the
program waits at most 5 more seconds for its output pipes to close, so a hung repository cannot
stall the whole run. A query that exceeds the limit is reported as a timeout for that repository,
and the remaining repositories are still checked.

That porcelain text is the displayed status in every mode, including `--json`. Porcelain is
Git's compact, script-friendly format, so its codes appear as-is:

```
--- Git status for api ---
 M internal/server.go
?? notes.txt

--- Git status for web ---

```

A clean repository prints its heading followed by a blank line, because its status text is
empty, and the report puts a blank line after each repository's status text. The codes
(`M` modified, `??` untracked, `A` added, and so on) are Git's own short status codes; the
handbook explains the common ones.

`--no-optional-locks` and `core.fsmonitor=false` keep this read-only query from taking optional
lock files or running the configured file-system monitor. They reduce incidental writes; they do
not isolate the query from Git or the repository. See "Trust and safety" below.

When the report goes to an interactive terminal, each `--- Git status for <name> ---` heading is
colored: green for a clean repository, yellow for a dirty one, and red when Git cannot inspect
the repository. Color is suppressed when output is redirected to a file or pipe, and when `-o`
is used.

Status rows, diagnostics, names, and error messages are escaped for display. Control characters
and Unicode bidirectional, line, and paragraph formatting characters become visible sequences
such as `\x1b`, `\u202e`, or `\u2028`. A repository name may contain a line or paragraph
separator, because those are not control characters; they are accepted and only escaped when
displayed. Status rows keep their real line breaks, so porcelain output still looks like Git wrote
it. Free-form messages, such as Git warnings and errors, are printed on one line with a literal
`\x0a` where they contained a line break. JSON output keeps the original values and relies on
JSON's own escaping.

Captured Git output is bounded; a truncated result is a failure. See "Output limits" below.

#### Git diagnostics

Git writes warnings to its error stream even when the status query succeeds. `repo-status` keeps
that text separate from the status: it never makes a clean repository look dirty, and it never
appears inside the porcelain text. Text output prints it after the status (or after the error
line when the query failed):

```
--- Git status for api ---
 M internal/server.go

Git diagnostics: warning: could not open directory 'vendor/'
```

When a query fails or is truncated, any status text Git had already written is still shown first,
then the `Error:` line, then the diagnostics. A failure never hides partial status output.

JSON output carries the same text in a `diagnostics` property. A diagnostic on its own does not
fail the run; only a failed query does.

Exactly one positional argument, the configuration file, is accepted. A missing or extra
argument prints usage and exits with status 1.

### `list`

```bash
repo-status list repos.yaml
repo-status list --json repos.yaml
```

Text output prints one line per repository, with the position used by selectors:

```
1: api (C:\path\to\api)
2: web (C:\path\to\web)
```

`--json` emits an array of objects with `index`, `name`, and `location` fields.

### `path`

```bash
repo-status path 1 repos.yaml
repo-status path api repos.yaml
repo-status path --name 42 repos.yaml
repo-status path --json api repos.yaml
```

Without `--name`, the selector is a position or a name; with `--name`, it is always an exact
name, which reaches numeric labels and labels beginning with `name:`.

The plain form prints the path with no trailing newline, so it can be used to change directory:

```powershell
cd $(repo-status path 1 repos.yaml)
```

When the output is not an interactive terminal, the path is written byte-exact, with no display
escaping, which keeps command substitution and scripts reliable. Terminal output escapes control
and bidirectional characters so the display cannot be corrupted. The JSON form emits an object
with a `path` property.

### `exec`

```bash
repo-status exec [options] <config_file> <command> [command_arguments...]
```

Options, placed before the configuration file:

- `--repos <selectors>`: comma-separated bare selectors (positions or exact names), for example
  `1,api`. An out-of-range number falls back to a name. Empty pieces are errors.
- `--names <labels>`: comma-separated exact names, with no position interpretation, for numeric
  labels and labels that begin with `name:`. `--names` and `--repos` are mutually exclusive.
- Either selection flag requires at least one entry. Every selector is resolved before anything
  runs, so one invalid or empty entry cancels the whole run, and a repository selected twice runs
  only once. Omitting both flags selects every configured repository.
- `--async`: run selected repositories in parallel, using a fixed pool of at most 8 workers
  whatever the number of repositories.
- `--dry-run`: print each target and command without running it.
- `--json`: emit a JSON array of command-result objects instead of text.

Everything after the configuration file is the command and is passed through unchanged, so the
command's own options need no escaping:

```bash
repo-status exec repos.yaml git status
repo-status exec --async repos.yaml git fetch
repo-status exec --repos 1,api repos.yaml git pull
repo-status exec --names 42,name:legacy repos.yaml git log --oneline -5
```

The command is started directly, not through a shell: pipes, redirection, `&&`, wildcards, and
variable references are not interpreted by `repo-status`. Run a shell explicitly when you need
them, for example `repo-status exec repos.yaml bash -c "..."` on Linux or macOS, or
`repo-status exec repos.yaml cmd /c "..."` on Windows.

`exec` puts no time limit on your command; a long build or fetch is allowed to finish. The
status view is different: it gives Git a fixed limit, described above.

Text output prints one block per repository:

```
--- Output for api ---
Already up to date.
```

The captured output is printed as-is and then followed by a newline, so blocks usually appear
separated by a blank line. When a command fails, its block contains an `Error:` line followed by
the command's own captured output. Repositories run one at a time in selection order unless
`--async` is given, in which case blocks appear in whatever order the commands finish.

`--dry-run` prints lines like `[DRY RUN] Would execute 'git fetch' in C:\path\to\api` in
selection order and starts no processes. In JSON mode the same line appears in each result's
`output` field. Use it to confirm both the command and the folders before a real run.

Each `--json` result is an object with `name`, `output`, and, when it failed, `error` fields.
Results stay in selection order even with `--async`.

`exec` reports every result and then returns exit status 1 if any command failed. The failing
command's own exit code is not reused.

## Output limits

Both commands that run a child process keep only a bounded beginning of its output, so a noisy
command cannot exhaust memory:

- Each target gets a share of a 64 MiB per-run budget, capped at 1 MiB: `min(1 MiB, 64 MiB ÷
  number of targets)`, with a floor of one byte. The number of targets is the configured
  repository count for status and the selected count for `exec`, so every target in a run gets
  the same share.
- Only the first bytes are kept; the rest are discarded while the child keeps running.
- For status, exceeding the limit is a failure that names the stream, for example `Git output
  truncated after 1048576 bytes (stdout)`. The retained prefix is still shown, the repository is
  not silently accepted, and the run exits with status 1.
- For `exec`, truncation appends `command output truncated after N bytes` to that result's
  `error`, after any command failure and separated by `; `. The retained prefix is still in the
  `output` field or text block, and the run exits with status 1.
- Truncation is reported per repository and does not stop the others.

Separately, rendered output is capped at 128 MiB:

- `list --json`, status output (text or JSON), `exec` JSON, and any report written with `-o` use
  the 128 MiB rendered-report limit.
- `exec` text has its own 128 MiB limit. It covers every streamed block, including `--dry-run`
  output, because long command text repeated per repository can add up.
- Plain `list` text and both `path` modes have no rendered limit.
- JSON gets a preflight pass through the limiter before anything reaches real output, so an
  over-limit JSON report fails without leaving a partial array behind.
- Text is streamed, so an over-limit text report may already have printed earlier complete blocks
  before it fails with status 1. A block that does not fit is not written.
- `exec` text is also bounded by the capture limits above, which bound each block's command
  output.

## Writing the report to a file

`-o <output_file>` writes the status report to a new file. The destination is never truncated,
replaced, or written through:

1. Before running any Git query, the destination is checked and rejected if anything already
   exists there: a file, folder, symbolic link, or hard link, including the configuration file
   itself.
2. Every Git result is collected before any output file exists, and only then is the report
   rendered into the temporary file. A report written inside a repository therefore cannot appear
   in its own status.
3. The report is written to a temporary file in the destination's folder, flushed, closed, and
   then published under the requested name by creating a hard link. The temporary name has a
   fixed shape and does not reuse the destination's name. Creating a link fails if anything
   appeared at the destination in the meantime, so a late collision is reported rather than
   overwritten.

On Windows, a destination written with alternate data stream syntax (`report.txt:stream`) is
rejected before any file is created.

Consequences:

- The destination's folder must already exist, be writable, and be one you trust. A relative
  name is relative to the current folder.
- The filesystem must support hard links, as local NTFS, APFS, ext4, and similar filesystems do.
  After the link succeeds the temporary name is removed; the report remains as an ordinary file,
  keeping the temporary file's permissions (owner-only where the platform applies Unix-style
  permissions).
- If any step fails, including a rendered report over the 128 MiB limit, the run exits with
  status 1, the temporary file is removed, and the existing destination (if any) is untouched.
- Shell redirection is different: `repo-status repos.yaml > report.txt` is handled by the shell,
  which truncates an existing file and follows symbolic links before `repo-status` runs. Use
  `-o` when you want the no-clobber behavior above.

## JSON output

- All JSON output is indented with two spaces and is not followed by a newline.
- Properties marked optional are omitted unless they have a value.
- Status objects have `name`, `status`, and, when present, `diagnostics` and `error`.
- Exec results have `name`, `output`, and, when present, `error`.
- List objects have `index`, `name`, and `location`; `path` emits one object with `path`.
- With `--dirty`, clean repositories are left out of the status array, while repositories that
  failed to report status are kept.

## Trust and safety

`repo-status` runs external programs and reads paths from a configuration file. Treat the tool,
the Git installation, and the configured repositories as trusted inputs.

- **Status and `exec` need trusted Git, repositories, and environment.** The status view starts
  Git in each configured folder. Git reads system, global, and repository configuration and
  environment variables such as the `GIT_*` settings, so settings on your machine and inside the
  repository affect the result. A repository you would not trust with your shell is a repository
  you should not add here. `--no-optional-locks` and `core.fsmonitor=false` only avoid optional
  writes; they are not a sandbox.
- **Git may use a parent repository.** If a configured location is a folder inside a repository
  rather than its top folder, Git finds the enclosing repository and reports that repository's
  state. A location need not contain a `.git` folder of its own.
- **`exec` runs with your permissions and is not isolated.** The command starts in each selected
  folder as your user, with your environment and network access. There is no sandbox, container,
  privilege drop, or output filtering: the command can do anything you can, and its output is
  passed through unmodified. Only run commands and repositories you trust.
- **Preview with `--dry-run`.** It prints the exact command and folder for every selected
  repository without starting anything, which is the safest way to check a new configuration or
  a destructive command.
- **`list` and `path` do not run Git.** They only read and validate the configuration file, so
  they are safe to use before deciding whether a repository is trustworthy.
- **Writing the report is a link operation.** It creates a new entry in the destination folder,
  so that folder must be one you trust; see "Writing the report to a file".
- **Timeouts and output limits are reliability guards.** The 30-second Git limit and the capture
  budgets keep a hang or a flood of output from blocking a run. They do not restrict what Git or
  your command is allowed to do, and they are not a sandbox.

## Errors and exit status

| Exit status | Meaning |
| --- | --- |
| 0 | The requested work completed without errors. |
| 1 | Bad usage, an invalid configuration, an invalid or empty selector, `--repos` and `--names` used together, an output destination that already exists or cannot be published, a failed output write, output over a rendered limit (JSON, status, or streamed `exec` text), a failed or timed-out Git status check, truncated captured output, or a failed `exec` command. |
| 2 | An option was not recognized or could not be parsed. |

Representative messages:

| Message | Cause |
| --- | --- |
| `Error: --repos requires at least one repository selector` | `--repos` was supplied but empty. |
| `Error: --names requires at least one repository name` | `--names` was supplied but empty. |
| `Error: --repos and --names are mutually exclusive` | Both selection flags were given. |
| `Error: empty repository selector` | A `--repos` list such as `1,,2` contains an empty piece. |
| `Error: empty repository name` | A `--names` list contains an empty piece. |
| `Error: repository name is empty` | `path --name` was given an empty label. |
| `Error: repository not found: api` | No repository has that exact name. |
| `Error: index out of range: 5` | A number is not a configured position and matches no name. |
| `Error reading config file: ...` | The file could not be read or did not pass validation. |
| `Error reading config file: config file must be a regular file: ...` | The path is a directory, device, or pipe. |
| `Error reading config file: alternate data stream paths are not supported: ...` | Windows stream syntax was used for the configuration file. |
| `Error: output file already exists; refusing to overwrite: ...` | The `-o` destination exists. |
| `Error: alternate data stream paths are not supported: ...` | Windows stream syntax was used for `-o`. |
| `Error: checking status timed out after 30s: ...` | Git did not answer within the per-repository limit. |
| `Error: Git output truncated after 1048576 bytes (stdout)` | Git wrote more than the per-target share; the retained prefix is shown. |
| `Error: exit status 1; command output truncated after 1048576 bytes` | An `exec` command failed and its output was capped. |
| `Error writing status output: ... rendered output exceeds the per-run limit (134217728 bytes)` | The report would exceed the 128 MiB rendered cap; an `-o` report is not published. |
| `Error writing exec output: ... rendered output exceeds the per-run limit (134217728 bytes)` | Streamed `exec` text would exceed its 128 MiB cap; earlier blocks may already be printed. |
| `Error writing status output: ...` | The report could not be written or published. |
| `Error writing list output: ...` (also `path`, `exec`) | The output stream rejected a write. |

Running `repo-status` with no arguments, or with the wrong number of arguments for a
subcommand, prints usage to standard error and exits with status 1.

Every output write is checked, including writes to a terminal. If output fails, the run reports
the error and exits with status 1 instead of pretending to succeed.

Messages written to standard error are display-escaped as well: control, bidirectional, line, and
paragraph formatting characters become visible sequences, and a line break inside a free-form
message becomes a literal `\x0a`, so one message stays on one line. Status rows keep their own
line breaks. JSON keeps original values, and the output of an `exec` command is passed through
unchanged because you chose that command.

A Git status failure is reported once for the affected repository: in text output as an `Error:`
line followed by a separate `Git diagnostics:` line when Git wrote anything, and in JSON in the
`error` and `diagnostics` fields. The raw status text is not mixed with the diagnostics. The rest
of the report is still produced, including a report written with `-o`, and the run then exits
with status 1.

Timeouts and truncation follow the same pattern: they are reported in `error`, not as
diagnostics, and only the repository that hit the limit is affected. A truncated status counts
as a failure even when Git itself exited successfully.

## Further reading

- `handbook/user-guide.md`: task-by-task guide, including common status codes and
  troubleshooting.
- `handbook/code-walkthrough.md`: a plain-language tour of how the program is built.

## License

MIT; see `LICENSE`.
