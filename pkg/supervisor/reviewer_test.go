package supervisor

import (
	"testing"

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
