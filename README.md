# protoworks-repo-status

A command-line tool for managing multiple git repositories. Check status, list repositories, and execute commands across them.

## Usage

1.  Create an index file (e.g., `repos.index`) with a list of paths to your git repositories, one per line.
2.  Run the program with one of the available commands.

**Note:** For easier use, you can add the `repo-status.exe` executable to your system's PATH. This will allow you to run the `repo-status` command from any directory.

### Commands

#### Default (git status)

Show the git status for all repositories in the index file.

```bash
go run main.go repos.index
```

Or, build the executable first:

```bash
go build .
./repo-status.exe repos.index
```

**Options**

-   `-o <output_file>`: Write the output to a file instead of stdout.

    ```bash
    ./repo-status.exe -o status.log repos.index
    ```

#### `list`

List all repositories and their numerical positions from the index file.

```bash
./repo-status.exe list repos.index
```

#### `path`

Get the path of a repository at a given index.

```bash
./repo-status.exe path <index> <index_file>
```

**Tip:** You can use the output of this command to `cd` into a repository directory.

**PowerShell**
```powershell
cd $(./repo-status.exe path 1 repos.index)
```

**bash**
```bash
cd $(./repo-status.exe path 1 repos.index)
```

#### `exec`

Execute a command in each repository directory.

```bash
./repo-status.exe exec <index_file> <command>
```

**Options**

-   `-repos <positions>`: A comma-separated list of repository positions to run the command on. If not specified, the command will run on all repositories.

    ```bash
    # Run 'git pull' on the 1st and 3rd repositories in the index
    ./repo-status.exe exec -repos "1,3" <index_file> git pull
    ```
