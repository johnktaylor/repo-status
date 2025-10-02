package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	listCommand := flag.NewFlagSet("list", flag.ExitOnError)
	execCommand := flag.NewFlagSet("exec", flag.ExitOnError)
	pathCommand := flag.NewFlagSet("path", flag.ExitOnError)

	execRepos := execCommand.String("repos", "", "Comma-separated list of repo positions to run the command on")

	switch os.Args[1] {
	case "list":
		listCommand.Parse(os.Args[2:])
		if len(listCommand.Args()) != 1 {
			fmt.Fprintf(os.Stderr, "Usage: %s list <index_file>\n", os.Args[0])
			os.Exit(1)
		}
		indexFile := listCommand.Args()[0]
		repos, err := readRepos(indexFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading index file: %v\n", err)
			os.Exit(1)
		}
		for i, repo := range repos {
			fmt.Printf("%d: %s\n", i+1, repo)
		}
	case "path":
		pathCommand.Parse(os.Args[2:])
		if len(pathCommand.Args()) != 2 {
			fmt.Fprintf(os.Stderr, "Usage: %s path <index> <index_file>\n", os.Args[0])
			os.Exit(1)
		}
		indexStr := pathCommand.Args()[0]
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid index: %v\n", err)
			os.Exit(1)
		}

		indexFile := pathCommand.Args()[1]
		repos, err := readRepos(indexFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading index file: %v\n", err)
			os.Exit(1)
		}

		if index < 1 || index > len(repos) {
			fmt.Fprintf(os.Stderr, "Index out of range\n")
			os.Exit(1)
		}

		fmt.Print(repos[index-1])
	case "exec":
		execCommand.Parse(os.Args[2:])
		if len(execCommand.Args()) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: %s exec [options] <index_file> <command>\n", os.Args[0])
			execCommand.PrintDefaults()
			os.Exit(1)
		}
		indexFile := execCommand.Args()[0]
		command := execCommand.Args()[1:]

		repos, err := readRepos(indexFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading index file: %v\n", err)
			os.Exit(1)
		}

		var targetRepos []string
		if *execRepos != "" {
			positions, err := parseRepoPositions(*execRepos)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid repo positions: %v\n", err)
				os.Exit(1)
			}
			for _, pos := range positions {
				if pos > 0 && pos <= len(repos) {
					targetRepos = append(targetRepos, repos[pos-1])
				}
			}
		} else {
			targetRepos = repos
		}

		for _, repoPath := range targetRepos {
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Dir = repoPath
			out, err := cmd.CombinedOutput()

			fmt.Printf("--- Output for %s ---\n", repoPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			fmt.Println(string(out))
		}
	default:
		output := flag.String("o", "", "Output file path")
		flag.Parse()

		if len(flag.Args()) == 0 {
			printUsage()
			os.Exit(1)
		}
		indexFile := flag.Args()[0]

		var writer io.Writer
		if *output != "" {
			f, err := os.Create(*output)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()
			writer = f
		} else {
			writer = os.Stdout
		}

		repos, err := readRepos(indexFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading index file: %v\n", err)
			os.Exit(1)
		}

		for i, repoPath := range repos {
			cmd := exec.Command("git", "status")
			cmd.Dir = repoPath
			out, err := cmd.CombinedOutput()

			fmt.Fprintf(writer, "--- Git status for %d: %s ---\n", i+1, repoPath)
			if err != nil {
				fmt.Fprintf(writer, "Error: %v\n", err)
			}
			fmt.Fprintln(writer, string(out))
		}
	}
}

func readRepos(indexFile string) ([]string, error) {
	file, err := os.Open(indexFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var repos []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		repoPath := scanner.Text()
		if repoPath == "" {
			continue
		}

		if !filepath.IsAbs(repoPath) {
			repoPath, err = filepath.Abs(filepath.Join(filepath.Dir(indexFile), repoPath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting absolute path for %s: %v\n", repoPath, err)
				continue
			}
		}
		repos = append(repos, repoPath)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return repos, nil
}

func parseRepoPositions(posStr string) ([]int, error) {
	var positions []int
	parts := strings.Split(posStr, ",")
	for _, part := range parts {
		pos, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		positions = append(positions, pos)
	}
	return positions, nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [options] <index_file>\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  list: List repositories and their positions")
	fmt.Fprintln(os.Stderr, "  path: Get the path of a repository at a given index")
	fmt.Fprintln(os.Stderr, "  exec: Execute a command in repository directories")
	fmt.Fprintln(os.Stderr, "  (default): Show git status for all repositories")
}
