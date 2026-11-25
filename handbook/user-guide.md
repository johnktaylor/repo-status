# repo-status user guide

This guide is for people who use `repo-status`: what it does, how to set it up, and how to read
what it prints. It uses plain language and explains any technical term it cannot avoid.

This handbook is written for human readers. It is not the authority on how the program behaves,
and it is not a knowledge source for automated assistants. The Go source, the tests,
`README.md`, and `repos.yaml.template` are what the program actually is. When this guide and the
program disagree, the program is right and the guide should be fixed.

If you want to understand how the program is built rather than how to use it, read
`code-walkthrough.md` instead.

## What problem it solves

Most of us work with several Git repositories at once: a service, its client, some shared
libraries. Finding out "what is uncommitted anywhere?" or "did the fetch succeed everywhere?"
normally means opening each folder in turn.

`repo-status` answers those questions from one list. You write the folders down once, then ask:

- what is the status of every repository?
- which repositories have changes I have not committed?
- where is the folder for the repository I call `api`?
- run `git fetch` in all of them, or only in two of them.

`repo-status` reads your configuration file, asks Git for status, or runs a command you name. It
does not clone, commit, push, or change configuration on its own.

## Before you start

You need:

- Git installed and available on your `PATH` for the status view, version 2.36 or newer
  recommended (`git --version`). The status check turns off a Git feature with the setting
  `core.fsmonitor=false`. Git 2.35.1 and older read that value as the name of a helper program to
  run rather than as an on/off value, per Git's own documentation, so newer Git is safer.
  `repo-status` does not check the version; it runs whatever `git` it finds.
- `exec` needs only the program you name. Git is needed for `exec` only when your command is a Git
  command.
- A built copy of `repo-status`, or Go installed so you can run it from source.

To build from source, in the repository folder:

```bash
go build .
```

That creates `repo-status` (on Windows, `repo-status.exe`) in the same folder. To run without
building, use `go run .` followed by the arguments. The examples below use `repo-status`; add
`.exe` on Windows.

`list` and `path` do not need Git. They only read your configuration file.

## Create your configuration file

1. Copy `repos.yaml.template` to a file named `repos.yaml`.
2. Replace the example entry with your own repositories.
3. Add one entry per repository.

```yaml
repositories:
  - name: api
    location: C:\path\to\api
  - name: web
    location: ..\web
```

Each entry has two parts:

- `name`: the label you use on the command line and the label printed in output. Pick something
  short that you will recognize. Two entries cannot share a name, even if they differ only in
  capital letters.
- `location`: the folder that holds the repository, either a full path or a path relative to the
  folder containing `repos.yaml`.

YAML is the file format; the indentation carries meaning and each list item starts with `-`.
Keep the two-space indentation shown above. Keep the file out of version control: the project's
`.gitignore` already ignores local YAML files, because the paths are specific to your machine.

### What the file must look like

`repo-status` checks the file before doing anything else. It refuses the file when:

- the path is not a regular file, such as a folder, device, or pipe;
- there is a second YAML document after a `---` line;
- an unknown or misspelled setting appears; only `repositories`, `name`, and `location` are
  allowed;
- there are no repositories at all, including an empty file or one with only comments;
- there are more than 1,000 repositories;
- the file is larger than 1 MiB (1,048,576 bytes); reading stops at the limit, so a huge or
  growing file is refused instead of being read in full;
- a `name` or `location` is blank, or only spaces;
- two names match ignoring capital letters;
- a name contains a comma, an invisible control character such as a tab or newline, or a Unicode
  bidirectional formatting character that could make the displayed name read in a misleading
  order.

The comma and the invisible characters are refused because commas separate `--repos` selectors and
because those characters cannot be shown or typed safely.

On Windows, a location must be a complete path. `C:repos` (a path relative to a drive's current
folder), `\repos` (a path on the current drive), and `\\server` (a server name with no share) are
all rejected instead of being guessed at. Use `C:\path\to\repos`, a relative path, or a complete
share path such as `\\server\share\repos`. A Windows alternate data stream path such as
`repos.yaml:stream` is also refused, for the configuration file, for a location, and for the `-o`
output destination.

### Choose a repository

`path` and `exec --repos` accept a position or a name:

| Selector | Chooses |
| --- | --- |
| `1` | The first repository in the file. |
| `api` | The repository named exactly `api`. Capital letters count. |
| `name:api` | A repository literally named `name:api`; `name:` has no special meaning here. |

A whole number is treated as a position when it is between 1 and the number of repositories. If it
is out of range, it is matched against names instead. Anything that is not a number has to match a
name exactly, capital letters included. Spaces around a selector are ignored.

Two options skip the position rule and always mean names, which is how you reach a repository
whose label is a number or begins with `name:`:

```bash
repo-status path --name 42 repos.yaml
repo-status path --name name:legacy repos.yaml
repo-status exec --names api,web repos.yaml git status
```

`path --name` takes one label. `exec --names` takes a comma-separated list and cannot be combined
with `--repos`. With `--repos`, separate selectors with commas: `--repos 1,web`. An empty piece
such as `1,,2`, or a flag with no value at all, is an error and nothing runs.

## Everyday tasks

Every command needs the path to your configuration file as a positional argument. Options always
come before that path: `repo-status --dirty repos.yaml` works, while
`repo-status repos.yaml --dirty` does not, because everything after the configuration file stops
being treated as an option. That rule is what lets `exec` hand a command's own options straight
through.

### See the status of everything

```bash
repo-status repos.yaml
```

You get one block per repository, in the order the entries appear in the file:

```
--- Git status for api ---
 M internal/server.go
?? notes.txt

--- Git status for web ---

```

`web` is clean, which is why its block is a heading and then a blank line: Git's status text for
a clean repository is empty. A blank line also separates one repository's block from the next.

When you run this in a terminal, each heading is colored: green for a clean repository, yellow
for one with changes, red when Git could not read the repository. Color is dropped automatically
when output goes to a file or a pipe, so scripts never see it.

### See Git's warnings

Sometimes Git writes a warning even though the status itself worked. `repo-status` keeps that
warning out of the status and prints it as its own line:

```
--- Git status for api ---
 M internal/server.go

Git diagnostics: warning: could not open directory 'vendor/'
```

A warning line never makes a clean repository look dirty and never fails the run. It only tells
you what Git complained about.

### See only the repositories that need attention

```bash
repo-status --dirty repos.yaml
```

Clean repositories are left out. Repositories that could not be checked are still shown, so a
problem never disappears just because you asked for a shorter report.

### Save the report to a file

```bash
repo-status -o report.txt repos.yaml
```

The report goes into a new file instead of the screen, without color. `repo-status` protects
that file in three ways:

1. If anything already exists at that name, it refuses and changes nothing.
2. It collects the whole report first, so a report written inside a repository cannot appear in
   its own status.
3. It writes the text to a temporary file in the same folder and then gives it the name you
   asked for by adding a second name for the same file (a hard link). If something appears at
   your chosen name in the meantime, the program reports the collision instead of overwriting
   it.

That last step means the folder must already exist, be writable, be one you trust, and sit on a
filesystem that allows a file to have two names, as most local disks do. Some network shares and
removable formats do not.

This is different from the shell's `>` redirection, which empties an existing file and follows
shortcuts before `repo-status` even starts. Use `-o` when you want the protection described
here.

### Use the output from a script

Add `--json` to the status, `list`, `path`, or `exec` commands:

```bash
repo-status --json repos.yaml
```

You get machine-readable JSON: indented with two spaces, no trailing newline, and fields for
problems that are left out when there is nothing to report.

### See the repositories and their numbers

```bash
repo-status list repos.yaml
```

```
1: api (C:\path\to\api)
2: web (C:\path\to\web)
```

The number on the left is the position of the entry in the file, counting from 1. You can use
either that number or the exact name to select a repository elsewhere.

### Jump into a repository folder

```bash
repo-status path api repos.yaml
```

This prints the folder and nothing else, with no trailing newline, so you can use it to change
directory:

```powershell
cd $(repo-status path 1 repos.yaml)
```

In a shell that uses backticks instead of `$(...)`, wrap the same command in backticks.

The same rule applies: `repo-status path 2 repos.yaml` and `repo-status path web repos.yaml`
both print the folder for the second repository. If a label is a number or begins with `name:`,
add `--name` so it is read as a label rather than a position:

```bash
repo-status path --name 42 repos.yaml
```

When the output is not an interactive terminal, the path is written exactly as stored, with no
display escaping and no trailing newline. That keeps `cd $(...)` and script use reliable.
Terminal output escapes invisible characters so a crafted path cannot disturb the screen.

### Run the same command in several repositories

```bash
repo-status exec repos.yaml git status
repo-status exec --async repos.yaml git fetch
repo-status exec --repos 1,web repos.yaml git pull
repo-status exec --names web repos.yaml git log --oneline -5
```

The configuration file comes first, then the command. Everything after the configuration file is
handed to the command exactly as you typed it, so the command's own options need no escaping.

`--repos` narrows the set with positions or names; `--names` narrows it with exact names only,
which is how you reach a numeric label or a label beginning with `name:`. The two cannot be used
together, and either one needs a value. Without both, every configured repository is used. Each
entry is checked before anything runs, so one typo or empty piece cancels the whole run rather
than leaving half the repositories done. `--async` runs repositories in parallel, with at most
eight running at a time however many you selected. `--dry-run` shows what would run without
running it, in selection order; in JSON mode the same line appears in each result's `output`
field:

```
[DRY RUN] Would execute 'git fetch' in C:\path\to\api
```

`repo-status` starts the command directly and does not use a shell, so pipes, `>`, `&&`,
wildcards, and `$VARIABLES` are not interpreted. If you need shell syntax, ask for a shell:

```bash
repo-status exec repos.yaml bash -c "git status && git log -1"
repo-status exec repos.yaml cmd /c "git status && git log -1"
```

The first form works on Linux and macOS, the second on Windows.

Each result starts with `--- Output for <name> ---`. When the command fails, an `Error:` line
follows the heading and the command's own output comes after it. Without `--async`, repositories
run one at a time in the order selected; with `--async` the blocks appear in whatever order the
commands finish.

There is no time limit on your command: a long build or fetch is allowed to finish. Output is
capped like status output. If a command writes more than its share, the error adds
`command output truncated after N bytes`, the retained beginning is still printed, and the run
exits with status 1. The whole text stream also has a 128 MiB limit; because text is printed block
by block, earlier blocks may already be on screen if that limit is reached.

Repository names, folders, and error lines are escaped for display, so invisible characters in a
name or message cannot disturb the terminal. The command's own output is not escaped: it is shown
exactly as the command produced it.

## Reading the status text

`repo-status` shows Git's own "porcelain" status text. Porcelain is the compact, stable format Git
provides for programs. Each changed file appears as two characters followed by the file name. The
first character describes what is staged for the next commit; the second describes what is not
staged yet. A space means "no change on that side".

| Text | Meaning |
| --- | --- |
| `?? file` | Git does not track this file yet. |
| ` M file` | The file changed, and the change is not staged. |
| `M  file` | The change is staged but not committed. |
| `MM file` | Some changes are staged and others are not. |
| `A  file` | A new file is staged. |
| ` D file` | The file was deleted, and the deletion is not staged. |
| `D  file` | The deletion is staged. |
| `R  old -> new` | A rename is staged. |
| `UU file` | Both sides changed the file and Git cannot merge it automatically. |

The spacing above is exact: those two characters before the file name carry the meaning. Git's
documentation lists the full set, including rarer codes.

Anything listed makes a repository "dirty" for `--dirty` and gives its heading the yellow color.
Empty status text means clean.

Each check is allowed 30 seconds. If Git does not answer in that time, the query is stopped and
that repository is reported as a timeout; a short extra wait (5 seconds) lets Git's output pipes
close, and the other repositories are still checked.

Only the beginning of a large result is kept. Each target gets at most 1 MiB, and less when many
repositories are selected: the run divides a 64 MiB budget across all of its targets. If Git
writes more than its share, the block shows an error naming the stream, for example
`Git output truncated after 1048576 bytes (stdout)`, and the run exits with status 1. A truncated
result is never treated as success.

Text output also hides invisible characters. Status rows keep their real line breaks, so the
porcelain list still looks like Git wrote it. Free-form messages, such as Git warnings and errors,
are printed on one line: a line break inside them appears as `\x0a`, and Unicode line or paragraph
separator characters are shown as escapes too. A repository name may contain a line or paragraph
separator, since those are not control characters; it is accepted and only escaped when displayed.
JSON keeps the original text and relies on JSON escaping.

Two output limits each allow 128 MiB. `list --json`, status output (text or JSON), `exec` JSON,
and any `-o` report use the rendered-report limit. `exec` text, including `--dry-run` lines, has
its own limit. Plain `list` text and both `path` modes have no rendered limit.

JSON is checked before anything is written to real output, so an over-limit JSON report fails
without leaving half an array behind. Text is streamed as complete blocks, so earlier blocks may
already be printed when the limit trips; the run then reports the problem and exits with status 1.

A few behaviors worth knowing:

- The tool runs `git status --porcelain` with two settings that only avoid optional writes,
  `--no-optional-locks` and `core.fsmonitor=false`. Everything else follows Git's normal
  behavior, including configuration from the machine, your user account, and the repository.
- If a `location` points at a folder inside a repository rather than at its top folder, Git finds
  the enclosing repository and reports that repository's state.
- Locations are not checked when the file is loaded. A wrong path shows up as an error in that
  repository's block, and the command exits with status 1.

## Is it safe to point at this repository?

`repo-status` is a convenience wrapper around Git and the commands you ask it to run. It does not
add a layer of protection between you and those programs.

- **Only use repositories, Git installations, and machines you trust.** Checking status runs
  Git inside each folder. Git reads settings from the system, your user account, and the
  repository, and environment variables can change what it does. The two flags used for status
  only reduce optional writes; they are not a sandbox.
- **A folder inside a repository counts as that repository.** Git looks upward for a repository,
  so a location does not need its own `.git` folder, and status covers the whole repository that
  contains it.
- **`exec` runs as you, with no isolation.** The command starts in each selected folder with your
  permissions, environment, and network access. The command's own output is passed through
  unfiltered, while names and error lines are escaped for display. Only run commands and
  repositories you trust; a runaway or malicious command can do anything you can do.
- **`--dry-run` first.** It prints the exact command and folder for every selected repository
  without starting anything. Check them before running something destructive.
- **`list` and `path` are read-only.** They do not start Git and only read your configuration
  file, so they are a safe way to inspect a setup you are unsure about.

## Exit status

Exit status is the number a program returns to the shell when it finishes; `0` means success and
anything else means something went wrong. It matters most in scripts.

| Status | Meaning |
| --- | --- |
| 0 | Everything requested succeeded. |
| 1 | Bad usage, a configuration problem, an invalid or empty selector, two selection flags at once, a report file that already exists or could not be published, a failed output write, output over a 128 MiB rendering limit, a failed or timed-out Git status check, output that was cut off at the capture limit, or a failed `exec` command. |
| 2 | An option was not recognized or could not be parsed. |

Some details:

- A failed `exec` command does not stop the remaining repositories. Every result is reported, and
  then the exit status is 1.
- A failing status check does not stop the other repositories, and the rest of the report is still
  produced, including one written with `-o`.
- If the output itself cannot be written, the run reports that and exits with status 1 rather
  than claiming success.
- The exact exit code of a command run by `exec` is not passed through. A failure is a failure,
  and you get 1.
- Running `repo-status` with no arguments prints the usage summary and exits with status 1.

## Troubleshooting

**"repo-status" is not recognized.** The binary is not on your `PATH`. Use its full path, or run
`go run .` from the source folder.

**`Error reading config file: ...`** The file could not be read or did not pass validation. The
rest of the message says why:

- an operating-system message, such as "The system cannot find the file specified", means the
  path to the configuration file is wrong;
- `config file contains no repositories` means the file is empty, contains only comments, or has
  no entries;
- `config file must be a regular file` means the path is a folder, device, or pipe;
- `alternate data stream paths are not supported` means Windows stream syntax such as
  `repos.yaml:stream` was used, which is refused for the file, a location, and `-o`;
- `config file must contain exactly one YAML document` means a second document was added after a
  `---` line;
- `config file is too large` means the file is over 1 MiB, or grew past the limit while read;
- `the maximum is 1000` means there are too many repository entries;
- `field ... not found` names an unknown or misspelled key, with its line number;
- `duplicate repository name found` means two entries share a name, ignoring letter case;
- `empty name` or `empty location` means an entry is missing that value or has only spaces;
- `must not contain a comma`, `must not contain control characters`, or `bidi formatting`
  means a name has a character that cannot be used or displayed safely;
- `drive-relative`, `drive-rooted`, or `incomplete UNC path` means a Windows location is not a
  complete path.

**A selector is not accepted.** The messages are specific:

- `repository not found` means no name matched, capital letters included;
- `index out of range` means a number was not a configured position and matched no name either;
- `empty repository selector` means a `--repos` list had a gap such as `1,,2`;
- `empty repository name` means a `--names` list had a gap;
- `repository name is empty` means `path --name` was given no label;
- `--repos requires at least one repository selector` or `--names requires at least one
  repository name` means the flag had no value;
- `--repos and --names are mutually exclusive` means both selection flags were given.

For a label that is a number or starts with `name:`, use `path --name` or `exec --names`. Run
`repo-status list repos.yaml` to see the positions and exact names.

**A status block shows `Error: checking status: ...`.** Git refused to look at that repository.
Any status text Git had already written appears first, then the error, then the separate
`Git diagnostics:` line with Git's own explanation, usually `not a git repository`. That usually
means the `location` points at the wrong folder or at a folder that is not a repository.

**A status block shows `Error: checking status timed out after 30s: ...`.** Git did not answer
within the limit, so the query was stopped. Very large working trees, slow disks, network folders,
or a stuck Git helper can cause this. The other repositories are still checked. The message is
about that query, not about your configuration.

**A result mentions `truncated after ... bytes`.** The program keeps only a limited beginning of
each target's output (at most 1 MiB, less when many targets are selected). Narrow the command,
for example a specific log range or a quieter status, if you need the rest. Truncation counts as a
failure, so the exit status is 1 even when the command itself succeeded.

**A message mentions `rendered output exceeds the per-run limit`.** The output would be larger than
128 MiB. That can be `list --json`, status output, `exec` JSON, or `exec` text (including dry-run
lines); only plain `list` text and `path` are outside those limits. Narrow the run, for example
with `--dirty`, a smaller configuration, or a quieter command. With `-o`, the report is not
published; with text output, earlier complete blocks may already be on screen.

**The status view complains about `core.fsmonitor` or a helper named `false`.** The installed Git
is older than 2.36 and reads the `false` setting as a program to run. Upgrade Git.

**An `exec` block shows `Error: exit status 128`.** The command ran but failed, and the lines
around the error are the command's own output. Exit status 128 is what Git uses for most fatal
errors. A message such as `chdir ...: no such file or directory` means the configured folder no
longer exists.

**`Error: output file already exists; refusing to overwrite: ...`** Pick a new name for `-o`, or
move the existing file. The program will not replace it, and shell `>` redirection would behave
differently.

**`Error writing status output: ...`** The report passed all the checks but could not be written
or published. Common causes are a folder that disappeared, a full disk, a read-only folder, or a
filesystem that does not allow hard links. Choose a local folder you control. The same wording
appears for the other commands as `Error writing list output: ...`, `Error writing path output:
...`, and `Error writing exec output: ...`.

**The report contains color codes.** Color is only used when output goes to an interactive
terminal. If you are capturing output it is already suppressed; if your terminal shows the codes
instead of color, write the report to a file with `-o` and read that.

**A path with spaces does not work in my script.** `repo-status` handles spaces in configured
paths, but your shell needs quotes around the result:
`cd "$(repo-status path api repos.yaml)"`.

## Word list

- **Repository**: a folder whose history Git tracks.
- **Git**: the version-control program that `repo-status` reads and runs.
- **Configuration file**: the YAML file where you list your repositories.
- **YAML**: a plain-text format for structured data, using indentation and `-` for lists.
- **Option (flag)**: a setting on the command line that starts with `-` or `--`.
- **Positional argument**: a command-line value that is not an option, such as the configuration
  file path.
- **Selector**: a number or name that picks one repository.
- **Porcelain status**: Git's compact status text meant for programs, such as ` M file`.
- **Dirty**: a repository with changes that are not committed, including untracked files.
- **Diagnostics**: messages Git writes to its error channel, kept separate from the status text.
- **JSON**: a text format for structured data that is easy for programs to read.
- **Exit status**: the number a program returns to the shell when it finishes.
- **Parallel (async)**: several commands running at the same time instead of one after another.
- **Dry run**: showing what would happen without running it.
- **Hard link**: a second name for the same file. Creating one never replaces an existing name.
- **Sandbox**: a restricted environment that limits what a program can touch. `repo-status` does
  not provide one.
