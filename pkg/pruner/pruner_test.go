package pruner

import (
	"strings"
	"testing"
)

func TestPruneANSI(t *testing.T) {
	colored := "\033[31mError:\033[0m Database connection \033[1;33mfailed\033[0m\n\n\n"
	opts := DefaultOptions()
	pruned, stats := Prune(colored, opts)

	if strings.Contains(pruned, "\033[") {
		t.Errorf("ANSI codes were not stripped: %s", pruned)
	}

	if stats.SavedBytes <= 0 {
		t.Errorf("Expected saved bytes > 0, got %d", stats.SavedBytes)
	}
}

func TestPruneConsecutiveBlankLines(t *testing.T) {
	input := "line 1\n\n\n\n\nline 2\n\n\nline 3"
	opts := DefaultOptions()
	opts.CollapseBlanks = true
	pruned, _ := Prune(input, opts)

	lines := strings.Split(pruned, "\n")
	emptyCount := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			emptyCount++
		}
	}

	if emptyCount > 2 {
		t.Errorf("Expected blank lines collapsed to at most 2, got %d", emptyCount)
	}
}

func TestPruneComments(t *testing.T) {
	code := `
package main

// This is a test comment
# Another comment
/* Block comment */
func main() {
    println("hello")
}
`
	opts := DefaultOptions()
	opts.Level = "code"
	opts.StripComments = true
	pruned, _ := Prune(code, opts)

	if strings.Contains(pruned, "This is a test comment") || strings.Contains(pruned, "Another comment") {
		t.Errorf("Comments were not removed: %s", pruned)
	}
	if !strings.Contains(pruned, `println("hello")`) {
		t.Errorf("Valid code was incorrectly pruned")
	}
}
