package pruner

import (
	"bufio"
	"regexp"
	"strings"
)

var (
	// Regex to remove ANSI color escape sequences
	ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

	// Code comment matchers
	lineCommentRegex = regexp.MustCompile(`^\s*(//|#|--|/\*|\*).*$`)

	// Repeated whitespace inside lines
	multiSpaceRegex = regexp.MustCompile(`[ \t]{2,}`)

	// Systemd / Syslog timestamp noise: e.g. "Sep 22 09:12:01 hostname app[1234]: "
	syslogHeaderRegex = regexp.MustCompile(`^[A-Z][a-z]{2}\s+\d+\s+\d{2}:\d{2}:\d{2}\s+[\w.-]+\s+[\w.-]+(\[\d+\])?:\s*`)
)

// Options configuration for context pruning.
type Options struct {
	Level          string // "basic", "aggressive", "code", "log"
	StripANSI      bool
	CollapseBlanks bool
	TrimTrailing   bool
	StripComments  bool
	SimplifyLogs   bool
}

// DefaultOptions returns standard pruning options.
func DefaultOptions() Options {
	return Options{
		Level:          "basic",
		StripANSI:      true,
		CollapseBlanks: true,
		TrimTrailing:   true,
		StripComments:  false,
		SimplifyLogs:   false,
	}
}

// Stats returns compression ratio metrics.
type Stats struct {
	OriginalBytes  int
	PrunedBytes    int
	SavedBytes     int
	SavingsPct     float64
	EstTokensSaved int
}

// Prune applies the configured pruning transformations to input text.
func Prune(input string, opts Options) (string, Stats) {
	origLen := len(input)
	if origLen == 0 {
		return "", Stats{}
	}

	result := input

	// 1. Strip ANSI escape sequences always
	if opts.StripANSI {
		result = ansiRegex.ReplaceAllString(result, "")
	}

	// Line-by-line processing
	scanner := bufio.NewScanner(strings.NewReader(result))
	var lines []string
	prevEmpty := false

	for scanner.Scan() {
		line := scanner.Text()

		if opts.TrimTrailing {
			line = strings.TrimRight(line, " \t\r")
		}

		// Log simplification
		if (opts.Level == "log" || opts.SimplifyLogs) && syslogHeaderRegex.MatchString(line) {
			line = syslogHeaderRegex.ReplaceAllString(line, "")
		}

		// Comment pruning
		if (opts.Level == "code" || opts.StripComments) && lineCommentRegex.MatchString(line) {
			continue
		}

		// Aggressive: collapse internal multiple spaces
		if opts.Level == "aggressive" {
			line = multiSpaceRegex.ReplaceAllString(line, " ")
		}

		isEmpty := strings.TrimSpace(line) == ""

		// Blank lines collapsing
		if opts.CollapseBlanks {
			if isEmpty {
				if prevEmpty {
					continue // Skip consecutive blank line
				}
				prevEmpty = true
			} else {
				prevEmpty = false
			}
		}

		lines = append(lines, line)
	}

	pruned := strings.Join(lines, "\n")
	prunedLen := len(pruned)
	saved := origLen - prunedLen
	if saved < 0 {
		saved = 0
	}

	savingsPct := 0.0
	if origLen > 0 {
		savingsPct = (float64(saved) / float64(origLen)) * 100.0
	}

	// Heuristic: ~4 bytes per token for English/Code/Logs
	estTokensSaved := saved / 4

	return pruned, Stats{
		OriginalBytes:  origLen,
		PrunedBytes:    prunedLen,
		SavedBytes:     saved,
		SavingsPct:     savingsPct,
		EstTokensSaved: estTokensSaved,
	}
}
