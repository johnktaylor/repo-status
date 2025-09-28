# repo-index

This is a Go project that provides a command-line interface to check the git status of multiple repositories listed in an index file.

## Usage

1.  Create an index file (e.g., `repos.index`) with a list of paths to your git repositories, one per line (paths can be relative to the index file).
2.  Run the program with the index file as an argument:

    ```bash
    go run main.go repos.index
    ```

    Or, build the executable first:

    ```bash
    go build .
    ./repo-index.exe repos.index
    ```

### Options

-   `-o <output_file>`: Write the output to a file instead of stdout.

    ```bash
    ./repo-index.exe -o status.log repos.index
    ```
