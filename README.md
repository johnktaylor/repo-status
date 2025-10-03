# protoworks-repo-status

A command-line tool for managing multiple git repositories. Check status, list repositories, and execute commands across them.

## Usage

1.  Create a YAML configuration file (e.g., `repos.yaml`) with a list of your git repositories. Each repository should have a `name`, `location`, and optional `locationtype` (defaulting to `local`).
2.  Run the program with one of the available commands.

**Note:** For easier use, you can add the `repo-status.exe` executable to your system's PATH. This will allow you to run the `repo-status` command from any directory.

### Commands

#### Default (git status)

Show the git status for all repositories in the config file.

```bash
go run main.go repos.yaml
```

Or, build the executable first:

```bash
go build .
repo-status.exe repos.yaml
```

**Options**

-   `-o <output_file>`: Write the output to a file instead of stdout.
-   `--json`: Output status information as a JSON object.

    ```bash
    repo-status.exe -o status.log --json repos.yaml
    ```

#### `list`

List all repositories and their numerical positions from the config file.

```bash
repo-status.exe list repos.yaml
```

**Options**

-   `--json`: Output the list of repositories as a JSON object.

#### `path`

Get the path of a repository at a given index or name.

```bash
repo-status.exe path <index_or_name> <config_file>
```

**Options**

-   `--json`: Output the path as a JSON object.

**Tip:** You can use the output of this command to `cd` into a repository directory.

**PowerShell**
```powershell
cd $(repo-status.exe path 1 repos.yaml)
```

**bash**
```bash
cd $(repo-status.exe path my-repo repos.yaml)
```

#### `exec`

Execute a command in each repository directory.

```bash
repo-status.exe exec <config_file> <command>
```

**Options**

-   `-repos <positions_or_names>`: A comma-separated list of repository positions or names to run the command on. If not specified, the command will run on all repositories.
-   `--dry-run`: Show what commands would be executed, without running them.
-   `--json`: Output the results of the execution as a JSON object.

    ```bash
    # Run 'git pull' on the 1st and 'my-repo' repositories in the config
    repo-status.exe exec -repos "1,my-repo" <config_file> git pull
    ```
