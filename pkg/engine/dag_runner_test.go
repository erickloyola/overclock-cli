package engine

import (
	"testing"
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
