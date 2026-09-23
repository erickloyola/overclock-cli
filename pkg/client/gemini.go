package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"overclock/pkg/pool"
)

const (
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta/models"
)

// GeminiClient handles API communication, connection pooling, and resilient retry logic.
type GeminiClient struct {
	httpClient *http.Client
	keyPool    *pool.KeyPool
	maxRetries int
}

// NewGeminiClient builds an HTTP client tuned for high concurrency and low latency.
func NewGeminiClient(keyPool *pool.KeyPool, maxRetries int, timeout time.Duration) *GeminiClient {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		MaxConnsPerHost:       128,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &GeminiClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		keyPool:    keyPool,
		maxRetries: maxRetries,
	}
}

// Execute performs the prompt request with automatic failover, backoff, and prefix preservation.
func (c *GeminiClient) Execute(ctx context.Context, opts RequestOptions) (*ExecutionResult, error) {
	// Build payload with prefix preservation for prompt caching
	payload, err := c.buildPayload(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build payload: %w", err)
	}

	var lastErr error
	var attempts int
	startTime := time.Now()

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		attempts = attempt

		// Acquire key from pool
		keyState, waitTime, err := c.keyPool.Acquire()
		if err != nil {
			return nil, fmt.Errorf("failed to acquire API key: %w", err)
		}

		// If key requires cooldown wait, apply jittered sleep
		if waitTime > 0 {
			jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime + jitter):
			}
		}

		// Execute HTTP call
		res, retriable, err := c.doRequest(ctx, keyState, opts, payload, startTime)
		if err == nil {
			c.keyPool.MarkSuccess(keyState)
			res.Retries = attempts
			return res, nil
		}

		lastErr = err

		if !retriable {
			c.keyPool.MarkError(keyState)
			return nil, err
		}

		// Apply Full Jitter Exponential Backoff
		// sleep = rand(0, min(maxBackoff, base * 2^attempt))
		backoff := c.computeJitterBackoff(attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return nil, fmt.Errorf("all %d retry attempts failed. Last error: %w", c.maxRetries, lastErr)
}

func (c *GeminiClient) computeJitterBackoff(attempt int) time.Duration {
	base := 1.0 * float64(time.Second)
	maxCap := 15.0 * float64(time.Second)
	multiplier := math.Pow(2, float64(attempt))
	currentCap := math.Min(maxCap, base*multiplier)

	// Full jitter: random between 0 and currentCap
	jitter := rand.Float64() * currentCap
	return time.Duration(jitter)
}

func (c *GeminiClient) buildPayload(opts RequestOptions) ([]byte, error) {
	req := GeminiRequest{
		GenerationConfig: &GenerationConfig{
			Temperature:     opts.Temperature,
			MaxOutputTokens: 8192,
		},
	}

	// 1. Prefix preservation: System instruction at root
	if opts.SystemPrompt != "" {
		req.SystemInstruction = &SystemInstruction{
			Parts: []Part{{Text: opts.SystemPrompt}},
		}
	}

	// 2. Prefix preservation: ContextData precedes variable user prompt for KV-cache reuse
	var parts []Part
	if opts.ContextData != "" {
		parts = append(parts, Part{Text: opts.ContextData})
	}
	if opts.Prompt != "" {
		parts = append(parts, Part{Text: opts.Prompt})
	}

	req.Contents = []Content{
		{
			Role:  "user",
			Parts: parts,
		},
	}

	return json.Marshal(req)
}

func (c *GeminiClient) doRequest(
	ctx context.Context,
	keyState *pool.KeyState,
	opts RequestOptions,
	payload []byte,
	overallStart time.Time,
) (*ExecutionResult, bool, error) {
	token, err := keyState.GetAuthToken()
	if err != nil {
		return nil, true, fmt.Errorf("falha ao obter token de autenticação: %w", err)
	}

	var url string
	if keyState.IsOAuth {
		// OAuth 2.0 uses standard Bearer header, clean URL
		if opts.Stream {
			url = fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse", geminiBaseURL, opts.Model)
		} else {
			url = fmt.Sprintf("%s/%s:generateContent", geminiBaseURL, opts.Model)
		}
	} else {
		// API Key query parameter
		if opts.Stream {
			url = fmt.Sprintf("%s/%s:streamGenerateContent?alt=sse&key=%s", geminiBaseURL, opts.Model, token)
		} else {
			url = fmt.Sprintf("%s/%s:generateContent?key=%s", geminiBaseURL, opts.Model, token)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "Overclock-CLI/1.0 (Linux; x86_64)")

	if keyState.IsOAuth {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Network errors are retriable
		return nil, true, fmt.Errorf("network request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle Rate Limit (HTTP 429) & Server Overload (HTTP 503 / 500)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == 500 {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		cooldown := c.keyPool.MarkRateLimited(keyState, retryAfter)
		return nil, true, fmt.Errorf("HTTP %d (RateLimit/Overloaded) on %s. Cooldown: %v",
			resp.StatusCode, keyState.DisplayLabel(), cooldown)
	}

	// Handle Non-200 Errors
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var apiErr GeminiResponse
		_ = json.Unmarshal(body, &apiErr)

		errMsg := string(body)
		if apiErr.Error != nil && apiErr.Error.Message != "" {
			errMsg = apiErr.Error.Message
		}

		// Check if quota error disguised as 403 or other status
		if strings.Contains(errMsg, "RESOURCE_EXHAUSTED") || strings.Contains(errMsg, "quota") {
			c.keyPool.MarkRateLimited(keyState, 30*time.Second)
			return nil, true, fmt.Errorf("quota exhausted on %s: %s", keyState.DisplayLabel(), errMsg)
		}

		// If OAuth token has insufficient scopes (HTTP 403), mark in cooldown so pool can failover to working API keys!
		if keyState.IsOAuth && (strings.Contains(errMsg, "insufficient authentication scopes") || strings.Contains(errMsg, "Insufficient Permission")) {
			c.keyPool.MarkRateLimited(keyState, 24*time.Hour)
			return nil, true, fmt.Errorf("conta OAuth %s com escopos insuficientes, alternando para chaves do pool: %s", keyState.DisplayLabel(), errMsg)
		}

		return nil, false, fmt.Errorf("gemini API error (HTTP %d): %s", resp.StatusCode, errMsg)
	}

	// Process 200 OK Response
	if opts.Stream {
		return c.handleSSEStream(resp.Body, keyState, opts, overallStart)
	}

	return c.handleStandardResponse(resp.Body, keyState, opts, overallStart)
}

func (c *GeminiClient) handleStandardResponse(
	body io.Reader,
	keyState *pool.KeyState,
	opts RequestOptions,
	overallStart time.Time,
) (*ExecutionResult, bool, error) {
	var geminiResp GeminiResponse
	if err := json.NewDecoder(body).Decode(&geminiResp); err != nil {
		return nil, false, fmt.Errorf("failed to decode response: %w", err)
	}

	var sb strings.Builder
	for _, cand := range geminiResp.Candidates {
		for _, part := range cand.Content.Parts {
			sb.WriteString(part.Text)
		}
	}

	text := sb.String()
	totalDuration := time.Since(overallStart)

	res := &ExecutionResult{
		Text:          text,
		Model:         opts.Model,
		UsedKeyMasked: keyState.DisplayLabel(),
		TotalDuration: totalDuration,
		TTFT:          totalDuration, // For non-streaming, TTFT equals total duration
	}

	if geminiResp.UsageMetadata != nil {
		res.PromptTokens = geminiResp.UsageMetadata.PromptTokenCount
		res.CandidatesTokens = geminiResp.UsageMetadata.CandidatesTokenCount
		res.TotalTokens = geminiResp.UsageMetadata.TotalTokenCount
		if totalDuration.Seconds() > 0 && res.CandidatesTokens > 0 {
			res.TokensPerSecond = float64(res.CandidatesTokens) / totalDuration.Seconds()
		}
	}

	return res, false, nil
}

func (c *GeminiClient) handleSSEStream(
	body io.Reader,
	keyState *pool.KeyState,
	opts RequestOptions,
	overallStart time.Time,
) (*ExecutionResult, bool, error) {
	scanner := bufio.NewScanner(body)
	// Buffer allocation for large tokens
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var fullText strings.Builder
	var firstTokenTime time.Time
	var promptTokens, candidateTokens, totalTokens int
	receivedAny := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		rawJSON := strings.TrimPrefix(line, "data: ")
		var chunk GeminiResponse
		if err := json.Unmarshal([]byte(rawJSON), &chunk); err != nil {
			continue
		}

		if chunk.UsageMetadata != nil {
			promptTokens = chunk.UsageMetadata.PromptTokenCount
			candidateTokens = chunk.UsageMetadata.CandidatesTokenCount
			totalTokens = chunk.UsageMetadata.TotalTokenCount
		}

		for _, cand := range chunk.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					if !receivedAny {
						receivedAny = true
						firstTokenTime = time.Now()
					}
					fullText.WriteString(part.Text)
					if opts.OnToken != nil {
						if err := opts.OnToken(part.Text); err != nil {
							return nil, false, err
						}
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, true, fmt.Errorf("stream scanning error: %w", err)
	}

	totalDuration := time.Since(overallStart)
	var ttft time.Duration
	if receivedAny {
		ttft = firstTokenTime.Sub(overallStart)
	} else {
		ttft = totalDuration
	}

	res := &ExecutionResult{
		Text:             fullText.String(),
		Model:            opts.Model,
		UsedKeyMasked:    keyState.DisplayLabel(),
		TTFT:             ttft,
		TotalDuration:    totalDuration,
		PromptTokens:     promptTokens,
		CandidatesTokens: candidateTokens,
		TotalTokens:      totalTokens,
	}

	if totalDuration.Seconds() > 0 && candidateTokens > 0 {
		res.TokensPerSecond = float64(candidateTokens) / totalDuration.Seconds()
	}

	return res, false, nil
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	// Try seconds as int
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Try HTTP date format
	if t, err := http.ParseTime(header); err == nil {
		diff := time.Until(t)
		if diff > 0 {
			return diff
		}
	}
	return 0
}
