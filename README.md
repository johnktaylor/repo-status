# protoworks-repo-status

`repo-status` checks Git status, lists repositories, finds repository paths, and runs commands across a configured set of repositories.

## Build and configuration

Build with `go build .`, or run from source with `go run . <arguments>`.

### YAML Configuration File

Start by copying `repos.yaml.template` to `repos.yaml` and replace its placeholders. `.yaml` files are excluded from Git tracking via `.gitignore` to avoid committing environment-specific local repository paths.

```yaml
repositories:
  - name: my-repository
    location: C:\path\to\repository
```

#### Fields

- `repositories`: List of repository configurations.
  - `name` *(string, required)*: Unique name for the repository.
  - `location` *(string, required)*: Path to the repository. Relative paths are resolved relative to the directory containing the configuration file.

Repository names must be unique. Locations cannot be blank, and relative paths are resolved relative to the configuration file. Only local repositories are supported.

Add one `- name` entry under `repositories` for each additional repository you want to manage.

## Commands

Place options before positional arguments.

### Default: Git status

```bash
repo-status.exe repos.yaml
go run . repos.yaml
```

- `--short`: use Git's short status format.
- `--dirty`: show only repositories with uncommitted or untracked changes.
- `-o <output_file>`: write output to a file; the configuration is validated before the file is created or truncated.
- `--json`: emit a JSON array of repository-status objects.

### `list`

```bash
repo-status.exe list repos.yaml
repo-status.exe list --json repos.yaml
```

`--json` emits an array with `index`, `name`, `location`, and `locationtype` fields.

### `path`

```bash
repo-status.exe path 1 repos.yaml
repo-status.exe path api repos.yaml
repo-status.exe path --json api repos.yaml
```

The JSON form emits an object with a `path` property. The plain form can be used to change directory:

```powershell
cd $(repo-status.exe path 1 repos.yaml)
```

### `exec`

```bash
repo-status.exe exec [options] <config_file> <command> [command_arguments...]
```

- `--repos <positions_or_names>`: comma-separated one-based indexes or exact names. If any selector is invalid, no command runs; duplicate selections run only once.
- `--async`: run selected repositories in parallel.
- `--dry-run`: show commands without running them.
- `--json`: emit a JSON array of command-result objects.

```bash
repo-status.exe exec --async repos.yaml git fetch
repo-status.exe exec --repos 1,api repos.yaml git fetch
```

`exec` returns a non-zero exit code if any invoked command fails, after reporting all results.

## Errors and exit status

Invalid configuration, invalid repository selectors, failed `exec` commands, and failed Git status commands return a non-zero exit status. Git-status failures appear in the text output or JSON `error` field, including with `--dirty`.
