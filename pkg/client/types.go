package client

import "time"

// Part represents an individual piece of text or data.
type Part struct {
	Text string `json:"text,omitempty"`
}

// Content represents a conversation turn.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// SystemInstruction holds the system prompt for prefix preservation.
type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

// GenerationConfig controls sampling parameters.
type GenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	TopP            float64 `json:"topP,omitempty"`
}

// GeminiRequest represents the payload sent to Google Gemini REST API.
type GeminiRequest struct {
	SystemInstruction *SystemInstruction `json:"system_instruction,omitempty"`
	Contents          []Content          `json:"contents"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
}

// Candidate represents a generated model response.
type Candidate struct {
	Content      Content `json:"content"`
	FinishReason string  `json:"finishReason"`
	Index        int     `json:"index"`
}

// UsageMetadata contains token usage metrics.
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// GeminiResponse represents the JSON response returned by Gemini.
type GeminiResponse struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	Error         *APIError      `json:"error,omitempty"`
}

// APIError details errors returned by Google API.
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// TokenCallback is invoked whenever a new text chunk is streamed.
type TokenCallback func(chunk string) error

// RequestOptions configures the execution of a prompt request.
type RequestOptions struct {
	Model        string
	SystemPrompt string
	Prompt       string
	ContextData  string // Static context/prefix to preserve for caching
	Temperature  float64
	Stream       bool
	OnToken      TokenCallback
}

// ExecutionResult holds output and telemetry.
type ExecutionResult struct {
	Text             string        `json:"text"`
	Model            string        `json:"model"`
	UsedKeyMasked    string        `json:"used_key"`
	TTFT             time.Duration `json:"ttft_ms"` // Time To First Token
	TotalDuration    time.Duration `json:"total_duration_ms"`
	PromptTokens     int           `json:"prompt_tokens"`
	CandidatesTokens int           `json:"candidates_tokens"`
	TotalTokens      int           `json:"total_tokens"`
	TokensPerSecond  float64       `json:"tokens_per_sec"`
	Retries          int           `json:"retries"`
}
