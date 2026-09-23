package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"overclock/pkg/client"
)

// AgyRunner executes prompts using the locally installed Antigravity CLI ('agy').
// This taps directly into the user's Gemini Pro / Google subscription with zero rate-limit restrictions.
type AgyRunner struct {
	binPath      string
	defaultModel string
	accountName  string
}

// NewAgyRunner initializes an AgyRunner, auto-detecting the agy binary if not provided.
func NewAgyRunner(binPath, defaultModel, accountName string) (*AgyRunner, error) {
	if binPath == "" {
		p, err := exec.LookPath("agy")
		if err != nil {
			home, _ := os.UserHomeDir()
			candidate := home + "/.local/bin/agy"
			if _, statErr := os.Stat(candidate); statErr == nil {
				p = candidate
			} else {
				return nil, fmt.Errorf("binário 'agy' não encontrado no PATH nem em ~/.local/bin/agy")
			}
		}
		binPath = p
	}

	if defaultModel == "" {
		defaultModel = "gemini-3.8-flash-high"
	}
	if accountName == "" {
		accountName = "default"
	}

	return &AgyRunner{
		binPath:      binPath,
		defaultModel: defaultModel,
		accountName:  accountName,
	}, nil
}

// Name returns the descriptive name of the engine.
func (r *AgyRunner) Name() string {
	return fmt.Sprintf("Antigravity Engine (%s [%s])", r.accountName, r.defaultModel)
}

// AccountName returns the active account name.
func (r *AgyRunner) AccountName() string {
	return r.accountName
}

// SetAccount changes the account profile for this runner.
func (r *AgyRunner) SetAccount(acc string) {
	r.accountName = acc
}

// Execute runs the prompt through 'agy', automatically choosing between CLI argument or STDIN stream-json
// to avoid Linux kernel E2BIG (argument list too long) limits on large prompts.
func (r *AgyRunner) Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error) {
	model := opts.Model
	if model == "" {
		model = r.defaultModel
	}

	// Build composite prompt
	var fullPrompt strings.Builder
	if opts.SystemPrompt != "" {
		fullPrompt.WriteString("[INSTRUÇÃO DO SISTEMA]\n")
		fullPrompt.WriteString(opts.SystemPrompt)
		fullPrompt.WriteString("\n\n")
	}
	fullPrompt.WriteString("[DIRETRIZ DE EXECUÇÃO]\nResponda diretamente com o texto e blocos de código solicitados. Não chame ferramentas externas nem execute comandos no terminal.\n\n")
	if opts.ContextData != "" {
		fullPrompt.WriteString("[DADOS DE CONTEXTO]\n")
		fullPrompt.WriteString(opts.ContextData)
		fullPrompt.WriteString("\n\n[SOLICITAÇÃO]\n")
	}
	fullPrompt.WriteString(opts.Prompt)

	promptStr := fullPrompt.String()

	// If prompt is large (> 32KB), use STDIN streaming mode to prevent Linux kernel argument list too long
	if len(promptStr) > 32*1024 {
		return r.executeViaStreamJSON(ctx, model, promptStr, opts)
	}

	args := []string{
		"-p", promptStr,
		"--dangerously-skip-permissions",
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, r.binPath, args...)
	cmd.Env = os.Environ()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("falha ao criar pipe stdout para agy: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		// Fallback to STDIN stream-json if Linux kernel still throws argument list too long
		if strings.Contains(err.Error(), "argument list too long") {
			return r.executeViaStreamJSON(ctx, model, promptStr, opts)
		}
		return nil, fmt.Errorf("falha ao iniciar agy: %w", err)
	}

	var outputBuf bytes.Buffer
	var ttft time.Duration
	firstToken := true
	readBuf := make([]byte, 1024)

	for {
		n, rErr := stdoutPipe.Read(readBuf)
		if n > 0 {
			if firstToken {
				ttft = time.Since(start)
				firstToken = false
			}
			chunk := string(readBuf[:n])
			outputBuf.WriteString(chunk)

			if opts.Stream && opts.OnToken != nil {
				_ = opts.OnToken(chunk)
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			break
		}
	}

	waitErr := cmd.Wait()
	totalDuration := time.Since(start)

	if waitErr != nil {
		stderrStr := strings.TrimSpace(stderrBuf.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("agy erro: %v | stderr: %s", waitErr, stderrStr)
		}
		return nil, fmt.Errorf("agy processo finalizado com erro: %w", waitErr)
	}

	rawText := outputBuf.String()
	promptTokens := len(promptStr) / 4
	candTokens := len(rawText) / 4
	totalTokens := promptTokens + candTokens
	var tps float64
	if totalDuration.Seconds() > 0 {
		tps = float64(candTokens) / totalDuration.Seconds()
	}

	return &client.ExecutionResult{
		Text:             rawText,
		Model:            model,
		UsedKeyMasked:    fmt.Sprintf("agy-account (%s)", r.accountName),
		TTFT:             ttft,
		TotalDuration:    totalDuration,
		PromptTokens:     promptTokens,
		CandidatesTokens: candTokens,
		TotalTokens:      totalTokens,
		TokensPerSecond:  tps,
		Retries:          0,
	}, nil
}

type agyStreamEvent struct {
	Event      string `json:"event"`
	StepUpdate *struct {
		StepType  string `json:"step_type"`
		TextDelta string `json:"text_delta"`
	} `json:"step_update,omitempty"`
	Result *struct {
		Status   string `json:"status"`
		Response string `json:"response"`
		Error    string `json:"error"`
		Usage    struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	} `json:"result,omitempty"`
}

// executeViaStreamJSON streams large prompts through STDIN to completely bypass OS command-line length limits.
func (r *AgyRunner) executeViaStreamJSON(
	ctx context.Context,
	model string,
	promptStr string,
	opts client.RequestOptions,
) (*client.ExecutionResult, error) {
	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, r.binPath, args...)
	cmd.Env = os.Environ()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("falha ao criar pipe stdin para agy: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("falha ao criar pipe stdout para agy: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	start := time.Now()
	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("falha ao iniciar agy em modo stream-json: %w", err)
	}

	// Send user prompt as NDJSON message over STDIN
	go func() {
		defer stdinPipe.Close()
		msg := map[string]interface{}{
			"event": "user",
			"message": map[string]string{
				"content": promptStr,
			},
		}
		data, err := json.Marshal(msg)
		if err == nil {
			data = append(data, '\n')
			_, _ = stdinPipe.Write(data)
		}
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	// Allocate buffer up to 16MB for large responses
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 16*1024*1024)

	var outputBuf strings.Builder
	var ttft time.Duration
	firstToken := true
	var inputTokens, outputTokens int

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev agyStreamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		if ev.StepUpdate != nil && ev.StepUpdate.TextDelta != "" {
			if firstToken {
				ttft = time.Since(start)
				firstToken = false
			}
			chunk := ev.StepUpdate.TextDelta
			outputBuf.WriteString(chunk)

			if opts.Stream && opts.OnToken != nil {
				_ = opts.OnToken(chunk)
			}
		}

		if ev.Result != nil {
			if ev.Result.Status == "ERROR" {
				return nil, fmt.Errorf("agy erro stream: %s", ev.Result.Error)
			}
			if ev.Result.Response != "" && outputBuf.Len() == 0 {
				outputBuf.WriteString(ev.Result.Response)
			}
			inputTokens = ev.Result.Usage.InputTokens
			outputTokens = ev.Result.Usage.OutputTokens
		}
	}

	waitErr := cmd.Wait()
	totalDuration := time.Since(start)

	if waitErr != nil {
		stderrStr := strings.TrimSpace(stderrBuf.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("agy stream erro: %v | stderr: %s", waitErr, stderrStr)
		}
		return nil, fmt.Errorf("agy processo stream finalizado com erro: %w", waitErr)
	}

	rawText := outputBuf.String()
	if inputTokens == 0 {
		inputTokens = len(promptStr) / 4
	}
	if outputTokens == 0 {
		outputTokens = len(rawText) / 4
	}
	totalTokens := inputTokens + outputTokens
	var tps float64
	if totalDuration.Seconds() > 0 {
		tps = float64(outputTokens) / totalDuration.Seconds()
	}

	return &client.ExecutionResult{
		Text:             rawText,
		Model:            model,
		UsedKeyMasked:    fmt.Sprintf("agy-account (%s) [stdin-stream]", r.accountName),
		TTFT:             ttft,
		TotalDuration:    totalDuration,
		PromptTokens:     inputTokens,
		CandidatesTokens: outputTokens,
		TotalTokens:      totalTokens,
		TokensPerSecond:  tps,
		Retries:          0,
	}, nil
}
