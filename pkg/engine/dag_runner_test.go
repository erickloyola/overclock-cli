package engine

import (
	"context"
	"testing"

	"overclock/pkg/client"
	"overclock/pkg/memory"
	"overclock/pkg/ui"
)

func TestExtractFilesFromResponse_UnfencedStart(t *testing.T) {
	// Simulated response where the LLM starts with `// file: ...` directly without opening backticks
	response := `// file: src/core/state.ts
export class StateManager {
  count = 0;
}
` + "```" + `

### Explicação Técnica da Implementação
1. Este é um texto explicativo que não deve entrar no código.
`

	extracted := extractFilesFromResponse(response, []string{"src/core/state.ts"})
	content, ok := extracted["src/core/state.ts"]
	if !ok {
		t.Fatalf("Esperava extrair src/core/state.ts, mas o arquivo não foi encontrado: %+v", extracted)
	}

	if !contains(content, "export class StateManager") {
		t.Errorf("Código real não foi extraído corretamente, obteve: %s", content)
	}

	if contains(content, "Explicação Técnica") {
		t.Errorf("Texto explicativo vazou para dentro do arquivo de código: %s", content)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || (len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

type mockRunner struct{}

func (m *mockRunner) Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error) {
	// Return a valid code block with a fact
	return &client.ExecutionResult{
		Text: "```go\n// file: main.go\npackage main\n\nfunc main() {}\n```\n[FACT:CONFIG:PORT] 8080\n",
	}, nil
}

func (m *mockRunner) Name() string {
	return "Mock Runner"
}

func TestRunDAGWithOptions_EventDrivenAndGates(t *testing.T) {
	bb := memory.NewBlackboard()
	bb.SetManifest(memory.ProjectManifest{
		Name:  "MockApp",
		Stack: "Go",
	})
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_stage1",
		Title:       "Stage 1 Setup",
		Stage:       1,
		TargetFiles: []string{"main.go"},
		Status:      memory.StatusPending,
	})

	term := ui.NewTerminal(false, false)
	opts := DAGOptions{
		Concurrency:  2,
		UseWorktrees: false,
	}

	err := RunDAGWithOptions(context.Background(), &mockRunner{}, bb, t.TempDir(), opts, term)
	if err != nil {
		t.Fatalf("RunDAGWithOptions falhou: %v", err)
	}

	task, ok := bb.GetTask("task_stage1")
	if !ok || task.Status != memory.StatusCompleted {
		t.Errorf("Esperava tarefa completada, obteve: %+v", task)
	}

	file, ok := bb.GetFile("main.go")
	if !ok || !contains(file.Content, "package main") {
		t.Errorf("Arquivo gerado não foi registrado corretamente no blackboard")
	}

	fact, ok := bb.GetFact("port")
	if !ok || fact.Value != "8080" {
		t.Errorf("Fato compartilhado não foi registrado: %+v", fact)
	}
}
