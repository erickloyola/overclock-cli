package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"overclock/pkg/client"
	"overclock/pkg/pruner"
)

// ANSI Color Codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
)

// Terminal handles formatting, telemetry output, and Unix pipeline friendliness.
type Terminal struct {
	isTTY      bool
	jsonOutput bool
	verbose    bool
	out        io.Writer
	errOut     io.Writer
}

// NewTerminal creates a terminal helper detecting TTY vs pipe.
func NewTerminal(jsonOutput, verbose bool) *Terminal {
	fi, err := os.Stdout.Stat()
	isTTY := false
	if err == nil {
		isTTY = (fi.Mode() & os.ModeCharDevice) != 0
	}

	return &Terminal{
		isTTY:      isTTY,
		jsonOutput: jsonOutput,
		verbose:    verbose,
		out:        os.Stdout,
		errOut:     os.Stderr,
	}
}

// IsTTY returns whether stdout is connected to a terminal.
func (t *Terminal) IsTTY() bool {
	return t.isTTY
}

// LogInfo prints diagnostic info to Stderr so as not to corrupt Unix pipelines.
func (t *Terminal) LogInfo(format string, args ...interface{}) {
	if !t.verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if t.isTTY {
		fmt.Fprintf(t.errOut, "%s[%sINFO%s]%s %s\n", Gray, Cyan, Gray, Reset, msg)
	} else {
		fmt.Fprintf(t.errOut, "[INFO] %s\n", msg)
	}
}

// LogWarn prints warning messages to Stderr.
func (t *Terminal) LogWarn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if t.isTTY {
		fmt.Fprintf(t.errOut, "%s[%sWARN%s]%s %s\n", Gray, Yellow, Gray, Reset, msg)
	} else {
		fmt.Fprintf(t.errOut, "[WARN] %s\n", msg)
	}
}

// LogError prints error messages to Stderr.
func (t *Terminal) LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if t.isTTY {
		fmt.Fprintf(t.errOut, "%s[%sERR%s]%s %s\n", Gray, Red, Gray, Reset, msg)
	} else {
		fmt.Fprintf(t.errOut, "[ERR] %s\n", msg)
	}
}

// StartThinking renders a subtle status indicator on stderr while waiting for TTFT.
func (t *Terminal) StartThinking(model string) {
	if t.isTTY && !t.jsonOutput {
		fmt.Fprintf(t.errOut, "%s⚡ Conectando a %s...%s\r", Cyan, model, Reset)
	}
}

// StopThinking clears the status indicator line.
func (t *Terminal) StopThinking() {
	if t.isTTY && !t.jsonOutput {
		fmt.Fprintf(t.errOut, "\r\033[K")
	}
}

// PrintChunk writes streaming token chunks directly to stdout.
func (t *Terminal) PrintChunk(chunk string) {
	if !t.jsonOutput {
		fmt.Print(chunk)
	}
}

// BatchItemResult structures batch output for JSON and Map modes.
type BatchItemResult struct {
	Index        int     `json:"index"`
	Source       string  `json:"source,omitempty"`
	Response     string  `json:"response"`
	TTFTMs       int64   `json:"ttft_ms"`
	DurationMs   int64   `json:"duration_ms"`
	TokensPerSec float64 `json:"tokens_per_sec"`
	PromptTokens int     `json:"prompt_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Retries      int     `json:"retries"`
	PruneSavings float64 `json:"prune_savings_pct,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// EmitResult outputs a single execution result according to output mode.
func (t *Terminal) EmitResult(source string, res *client.ExecutionResult, pruneStats *pruner.Stats) {
	if t.jsonOutput {
		item := BatchItemResult{
			Source:       source,
			Response:     res.Text,
			TTFTMs:       res.TTFT.Milliseconds(),
			DurationMs:   res.TotalDuration.Milliseconds(),
			TokensPerSec: res.TokensPerSecond,
			PromptTokens: res.PromptTokens,
			OutputTokens: res.CandidatesTokens,
			Retries:      res.Retries,
		}
		if pruneStats != nil && pruneStats.OriginalBytes > 0 {
			item.PruneSavings = pruneStats.SavingsPct
		}
		data, _ := json.Marshal(item)
		fmt.Fprintln(t.out, string(data))
		return
	}

	// In non-JSON mode:
	// If text was not already streamed, output the text directly
	if res.Text != "" {
		fmt.Fprintln(t.out, res.Text)
	}
}

// PrintBanner prints performance telemetry to Stderr so pipelines remain clean.
func (t *Terminal) PrintStats(res *client.ExecutionResult, pruneStats *pruner.Stats) {
	if !t.verbose && !t.isTTY {
		return
	}

	header := fmt.Sprintf("\n%s─── ⚡ OVERCLOCK STATS ───────────────────────────────%s\n", Gray, Reset)
	fmt.Fprint(t.errOut, header)

	if pruneStats != nil && pruneStats.OriginalBytes > 0 {
		fmt.Fprintf(t.errOut, " %sPrune:%s %d -> %d bytes (%s-%.1f%%%s | ~%d tokens)\n",
			Dim, Reset, pruneStats.OriginalBytes, pruneStats.PrunedBytes,
			Green, pruneStats.SavingsPct, Reset, pruneStats.EstTokensSaved)
	}

	fmt.Fprintf(t.errOut, " %sTTFT:%s %s%v%s  │  %sLatency:%s %v  │  %sSpeed:%s %s%.1f tok/s%s\n",
		Dim, Reset, Cyan, res.TTFT.Round(time.Millisecond), Reset,
		Dim, Reset, res.TotalDuration.Round(time.Millisecond),
		Dim, Reset, Green, res.TokensPerSecond, Reset)

	fmt.Fprintf(t.errOut, " %sTokens:%s in=%d out=%d total=%d  │  %sRetries:%s %d  │  %sKey:%s %s\n",
		Dim, Reset, res.PromptTokens, res.CandidatesTokens, res.TotalTokens,
		Dim, Reset, res.Retries,
		Dim, Reset, res.UsedKeyMasked)

	fmt.Fprintf(t.errOut, "%s─────────────────────────────────────────────────────%s\n", Gray, Reset)
}
