package engine

import (
	"context"
	"overclock/pkg/client"
)

// Runner represents any execution backend (Agy CLI or Gemini HTTP Client).
type Runner interface {
	Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error)
	Name() string
}

// GeminiClientRunner wraps *client.GeminiClient to implement Runner.
type GeminiClientRunner struct {
	client *client.GeminiClient
}

// NewGeminiClientRunner wraps client.GeminiClient.
func NewGeminiClientRunner(c *client.GeminiClient) *GeminiClientRunner {
	return &GeminiClientRunner{client: c}
}

func (r *GeminiClientRunner) Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error) {
	return r.client.Execute(ctx, opts)
}

func (r *GeminiClientRunner) Name() string {
	return "Gemini API (REST/HTTP2)"
}
