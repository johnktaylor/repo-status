# protoworks-repo-status

This is a Go project that provides a command-line interface to check the git status of multiple repositories listed in an index file.

## Usage

1.  Create an index file (e.g., `repos.index`) with a list of paths to your git repositories, one per line.
2.  Run the program with one of the available commands.

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

#### `exec`

Execute a command in each repository directory.

```bash
./repo-status.exe exec repos.index <command>
```

**Options**

-   `-repos <positions>`: A comma-separated list of repository positions to run the command on. If not specified, the command will run on all repositories.

    ```bash
    # Run 'git pull' on the 1st and 3rd repositories in the index
    ./repo-status.exe exec -repos "1,3" repos.index git pull
    ```
