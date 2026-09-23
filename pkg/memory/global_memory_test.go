package memory

import (
	"os"
	"testing"
)

func TestGlobalMemoryAndEvents(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test Events
	err := AppendEvent(tempDir, EventFactRecorded, map[string]string{"key": "test_port", "value": "3000"})
	if err != nil {
		t.Fatalf("AppendEvent falhou: %v", err)
	}

	err = AppendEvent(tempDir, EventFileRecorded, map[string]string{"path": "src/main.go"})
	if err != nil {
		t.Fatalf("AppendEvent falhou: %v", err)
	}

	events, err := LoadEvents(tempDir)
	if err != nil {
		t.Fatalf("LoadEvents falhou: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Esperava 2 eventos no log, obteve %d", len(events))
	}
	if events[0].Type != EventFactRecorded || events[1].Type != EventFileRecorded {
		t.Errorf("Tipos de eventos incorretos: %+v", events)
	}

	// 2. Test GlobalMemory
	os.Setenv("XDG_CONFIG_HOME", tempDir)
	defer os.Unsetenv("XDG_CONFIG_HOME")

	gm, err := LoadGlobalMemory()
	if err != nil {
		t.Fatalf("LoadGlobalMemory falhou: %v", err)
	}

	gm.SetFact(FactCategoryConvention, "CODE_STYLE", "Clean Architecture", "User")
	gm.AddConvention("Usar tipos imutáveis sempre que possível")

	if err := gm.Save(); err != nil {
		t.Fatalf("Save GlobalMemory falhou: %v", err)
	}

	// Re-load to ensure persistence
	gm2, err := LoadGlobalMemory()
	if err != nil {
		t.Fatalf("Re-load GlobalMemory falhou: %v", err)
	}
	fact, ok := gm2.GetFact("code_style")
	if !ok || fact.Value != "Clean Architecture" {
		t.Errorf("Fato global não persistido corretamente: %+v", fact)
	}
	if len(gm2.Conventions) != 1 {
		t.Errorf("Convenções globais não persistidas")
	}

	// Test Merge into Blackboard
	bb := NewBlackboard()
	bb.MergeGlobalMemory(gm2)

	bbFact, ok := bb.GetFact("code_style")
	if !ok || bbFact.Value != "Clean Architecture" {
		t.Errorf("MergeGlobalMemory falhou ao injetar fatos globais no Blackboard")
	}
	if len(bb.Manifest.Conventions) != 1 {
		t.Errorf("MergeGlobalMemory falhou ao injetar convenções")
	}
}
