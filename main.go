package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	output := flag.String("o", "", "Output file path")
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <index_file>\n", os.Args[0])
		flag.PrintDefaults()
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

	file, err := os.Open(indexFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening index file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

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

		cmd := exec.Command("git", "status")
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()

		fmt.Fprintf(writer, "--- Git status for %s ---\n", repoPath)
		if err != nil {
			fmt.Fprintf(writer, "Error: %v\n", err)
		}
		fmt.Fprintln(writer, string(out))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading repo.index: %v\n", err)
		os.Exit(1)
	}
}
