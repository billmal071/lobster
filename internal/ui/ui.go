// Package ui provides a secure fzf launcher abstraction.
// All items are piped to fzf via stdin as plain text — no shell-interpreted
// preview strings or commands with remote data.
package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Select presents items to the user via fzf and returns the selected item's index.
// Items are passed as plain text via stdin. No --preview or shell-evaluated strings.
func Select(prompt string, items []string) (int, error) {
	if len(items) == 0 {
		return -1, fmt.Errorf("no items to select from")
	}

	// Check if fzf is available
	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		return -1, fmt.Errorf("fzf not found in PATH: %w", err)
	}

	// Prepare numbered items for reliable index extraction
	var input strings.Builder
	for i, item := range items {
		fmt.Fprintf(&input, "%d\t%s\n", i, item)
	}

	// Build fzf command with safe arguments only
	cmd := exec.Command(fzfPath,
		"--prompt", prompt+" > ",
		"--height", "40%",
		"--reverse",
		"--with-nth", "2..", // Display from second field onward (hide index)
		"--delimiter", "\t",
		"--no-multi",
		"--cycle",
	)

	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return -1, fmt.Errorf("selection cancelled")
		}
		return -1, fmt.Errorf("fzf failed: %w", err)
	}

	selected := strings.TrimSpace(stdout.String())
	if selected == "" {
		return -1, fmt.Errorf("no selection made")
	}

	// Extract the index from the first tab-separated field
	parts := strings.SplitN(selected, "\t", 2)
	if len(parts) == 0 {
		return -1, fmt.Errorf("unexpected fzf output format")
	}

	var idx int
	if _, err := fmt.Sscanf(parts[0], "%d", &idx); err != nil {
		return -1, fmt.Errorf("parsing selection index: %w", err)
	}

	if idx < 0 || idx >= len(items) {
		return -1, fmt.Errorf("selection index %d out of range", idx)
	}

	return idx, nil
}

// Confirm asks the user a yes/no question via fzf.
func Confirm(prompt string) (bool, error) {
	idx, err := Select(prompt, []string{"Yes", "No"})
	if err != nil {
		return false, err
	}
	return idx == 0, nil
}

// Input prompts the user for free-text input via fzf's --print-query.
func Input(prompt string) (string, error) {
	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		return "", fmt.Errorf("fzf not found in PATH: %w", err)
	}

	cmd := exec.Command(fzfPath,
		"--prompt", prompt+" > ",
		"--height", "10%",
		"--reverse",
		"--print-query",
		"--no-info",
	)

	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = os.Stderr

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// fzf exits 1 when using --print-query with no match, which is expected
	_ = cmd.Run()

	query := strings.TrimSpace(strings.Split(stdout.String(), "\n")[0])
	if query == "" {
		return "", fmt.Errorf("no input provided")
	}

	return query, nil
}
