package supervisor

import (
	"context"
	"testing"

	"overclock/pkg/client"
	"overclock/pkg/memory"
)

func TestReviewProjectConsistency(t *testing.T) {
	bb := memory.NewBlackboard()

	// File with valid content
	bb.RecordFile("src/types.ts", "types", "export interface User { id: string; }", "worker-1")

	// File with relative import matching src/types.ts
	bb.RecordFile("src/UserCard.tsx", "component", "import { User } from './types';\nexport function UserCard() {}", "worker-2")

	// Empty file (< 15 chars)
	bb.RecordFile("src/empty.ts", "empty", "export {};", "worker-3")

	rep := ReviewProject(bb)

	if rep.TotalFilesChecked != 3 {
		t.Errorf("Esperava 3 arquivos checados, obteve: %d", rep.TotalFilesChecked)
	}

	if rep.Passed {
		t.Errorf("Esperava falha devido ao arquivo vazio src/empty.ts")
	}

	if len(rep.Errors) == 0 {
		t.Errorf("Esperava erro registrado para arquivo vazio")
	}
}

type mockPatchRunner struct {
	response string
	err      error
}

func (m *mockPatchRunner) Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &client.ExecutionResult{
		Text: m.response,
	}, nil
}

func (m *mockPatchRunner) Name() string {
	return "mock"
}

func TestReviewProjectBrokenImports(t *testing.T) {
	bb := memory.NewBlackboard()

	// JS/TS file with broken relative import
	bb.RecordFile("src/App.tsx", "component", "import { Missing } from './nonexistent';\nexport function App() {}", "worker-1")

	// Python file with broken relative import
	bb.RecordFile("pkg/service.py", "service", "from .unresolved import helper\n\ndef run():\n    pass\n", "worker-2")

	rep := ReviewProject(bb)

	if rep.Passed {
		t.Errorf("Esperava falha na verificação de consistência devido aos imports quebrados")
	}

	if len(rep.Errors) != 2 {
		t.Errorf("Esperava 2 erros de import relativo quebrado, obteve %d: %v", len(rep.Errors), rep.Errors)
	}

	notes := bb.GetNotes()
	if len(notes) != 2 {
		t.Fatalf("Esperava 2 notas registradas no Blackboard, obteve %d", len(notes))
	}

	for _, n := range notes {
		if n.Severity != "error" {
			t.Errorf("Esperava severidade 'error' na nota, obteve: %s", n.Severity)
		}
		if n.Fixed {
			t.Errorf("Nota não deveria estar marcada como corrigida inicialmente")
		}
	}

	// Test AutoPatchFile
	mock := &mockPatchRunner{
		response: "```tsx\nexport function App() { return <div>Fixed</div>; }\n```",
	}

	err := AutoPatchFile(context.Background(), mock, bb, "src/App.tsx", notes[0].Description, "test-model")
	if err != nil {
		t.Fatalf("AutoPatchFile retornou erro: %v", err)
	}

	updated, ok := bb.GetFile("src/App.tsx")
	if !ok {
		t.Fatalf("src/App.tsx não foi encontrado após patch")
	}
	if updated.Content != "export function App() { return <div>Fixed</div>; }" {
		t.Errorf("Conteúdo do patch inesperado: %s", updated.Content)
	}

	// Test MarkNotesFixed
	bb.MarkNotesFixed("src/App.tsx")
	notesAfter := bb.GetNotes()
	var appNoteFixed bool
	for _, n := range notesAfter {
		if n.File == "src/App.tsx" {
			appNoteFixed = n.Fixed
		}
	}
	if !appNoteFixed {
		t.Errorf("Esperava que nota de src/App.tsx estivesse marcada como Fixed")
	}
}
