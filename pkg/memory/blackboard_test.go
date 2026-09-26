package memory

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBlackboardConcurrency(t *testing.T) {
	bb := NewBlackboard()
	bb.SetManifest(ProjectManifest{
		Name:  "test-project",
		Stack: "Go + Vite",
	})

	var wg sync.WaitGroup
	// 50 concurrent writers and readers
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			bb.RecordFile(
				string(rune('a'+(idx%26)))+"/file.go",
				"purpose",
				"package main\nfunc Test() {}",
				"worker-1",
			)
			bb.SetContract("contract-1", "interface Model {}")
		}(i)

		go func(idx int) {
			defer wg.Done()
			_ = bb.GetManifest()
			_ = bb.GetContracts()
			_ = bb.GetAllFiles()
		}(i)
	}

	wg.Wait()

	files := bb.GetAllFiles()
	if len(files) == 0 {
		t.Errorf("Esperava arquivos gravados, obteve 0")
	}

	contracts := bb.GetContracts()
	if len(contracts) == 0 {
		t.Errorf("Esperava contratos gravados, obteve 0")
	}
}

func TestDAGDependencyResolution(t *testing.T) {
	bb := NewBlackboard()

	bb.RegisterTask(TaskNode{
		ID:          "task_1",
		Title:       "Configs & Types",
		TargetFiles: []string{"package.json", "types.ts"},
		DependsOn:   []string{},
	})

	bb.RegisterTask(TaskNode{
		ID:          "task_2",
		Title:       "Components",
		TargetFiles: []string{"Header.tsx"},
		DependsOn:   []string{"task_1"},
	})

	ready := bb.GetReadyTasks()
	if len(ready) != 1 || ready[0].ID != "task_1" {
		t.Fatalf("Esperava apenas task_1 pronta, obteve: %+v", ready)
	}

	// Complete task_1
	bb.UpdateTaskStatus("task_1", StatusCompleted, 100*time.Millisecond, "")

	readyAfter := bb.GetReadyTasks()
	if len(readyAfter) != 1 || readyAfter[0].ID != "task_2" {
		t.Fatalf("Esperava task_2 desbloqueada, obteve: %+v", readyAfter)
	}
}

func TestSaveAndLoadState(t *testing.T) {
	tempDir := t.TempDir()

	bb := NewBlackboard()
	bb.SetManifest(ProjectManifest{
		Name:           "car-game",
		Stack:          "HTML5 Canvas + TypeScript",
		PackageManager: "npm",
	})
	bb.RecordFile("src/main.ts", "Entrypoint", "console.log('start');", "Worker-1")
	bb.RegisterTask(TaskNode{
		ID:          "task_1",
		Title:       "Types",
		Status:      StatusCompleted,
		TargetFiles: []string{"src/types.ts"},
	})
	bb.RegisterTask(TaskNode{
		ID:          "task_2",
		Title:       "Game Loop",
		Status:      StatusPending,
		TargetFiles: []string{"src/loop.ts"},
		DependsOn:   []string{"task_1"},
	})

	if err := bb.SaveState(tempDir); err != nil {
		t.Fatalf("SaveState falhou: %v", err)
	}

	loadedBB, err := LoadState(tempDir + "/.overclock/state.json")
	if err != nil {
		t.Fatalf("LoadState falhou: %v", err)
	}

	if loadedBB.Manifest.Name != "car-game" {
		t.Errorf("Nome do projeto incorreto: %s", loadedBB.Manifest.Name)
	}

	file, ok := loadedBB.GetFile("src/main.ts")
	if !ok || file.Content != "console.log('start');" {
		t.Errorf("Arquivo não restaurado corretamente")
	}

	ready := loadedBB.GetReadyTasks()
	if len(ready) != 1 || ready[0].ID != "task_2" {
		t.Errorf("Esperava task_2 pronta no estado carregado, obteve: %+v", ready)
	}
}

func TestSharedFactsAndLessons(t *testing.T) {
	bb := NewBlackboard()

	// 1. Record Facts
	bb.RecordFact(FactCategoryConfig, "SERVER_PORT", "8080", "Worker-1")
	bb.RecordFact(FactCategoryDependency, "DATABASE_DRIVER", "pgx/v5", "Worker-2")

	facts := bb.GetFacts()
	if len(facts) != 2 {
		t.Fatalf("Esperava 2 fatos registrados, obteve %d", len(facts))
	}

	fact, ok := bb.GetFact("server_port")
	if !ok || fact.Value != "8080" {
		t.Errorf("Esperava SERVER_PORT 8080 normalizado, obteve: %+v", fact)
	}

	depFacts := bb.GetFactsByCategory(FactCategoryDependency)
	if len(depFacts) != 1 || depFacts[0].Key != "database_driver" {
		t.Errorf("Filtro por categoria falhou: %+v", depFacts)
	}

	summary := bb.FactsSummary()
	if !strings.Contains(summary, "SERVER_PORT: 8080") && !strings.Contains(summary, "server_port: 8080") {
		t.Errorf("FactsSummary não contém SERVER_PORT: %s", summary)
	}

	// 2. Record Lessons
	bb.RecordLesson("compiler", "undefined: fmt.Sprintf", "adicione import \"fmt\"", "pkg/storage/store.go")
	lessons := bb.GetLessons()
	if len(lessons) != 1 {
		t.Fatalf("Esperava 1 lição registrada, obteve %d", len(lessons))
	}

	lesSummary := bb.LessonsSummary()
	if !strings.Contains(lesSummary, "undefined: fmt.Sprintf") {
		t.Errorf("LessonsSummary não contém o erro esperado: %s", lesSummary)
	}
}

func TestFileRevisionsAndRollback(t *testing.T) {
	bb := NewBlackboard()

	// v1
	bb.RecordFile("src/service.ts", "Init", "export function hello() { return 'v1'; }", "Worker-1")
	f1, ok := bb.GetFile("src/service.ts")
	if !ok || f1.Version != 1 {
		t.Fatalf("Esperava versão 1, obteve %+v", f1)
	}

	// v2
	bb.RecordFile("src/service.ts", "Refactor", "export function hello() { return 'v2'; }", "Worker-2")
	f2, _ := bb.GetFile("src/service.ts")
	if f2.Version != 2 {
		t.Fatalf("Esperava versão 2, obteve %d", f2.Version)
	}

	revs := bb.GetFileRevisions("src/service.ts")
	if len(revs) != 1 || revs[0].Version != 1 {
		t.Fatalf("Esperava histórico com versão 1 arquivada, obteve %+v", revs)
	}

	// Rollback to v1
	err := bb.RollbackFile("src/service.ts", 1)
	if err != nil {
		t.Fatalf("Rollback falhou: %v", err)
	}

	fRollback, _ := bb.GetFile("src/service.ts")
	if !strings.Contains(fRollback.Content, "'v1'") {
		t.Errorf("Rollback não restaurou conteúdo de v1: %s", fRollback.Content)
	}
	if fRollback.Version != 3 {
		t.Errorf("Rollback deveria gerar versão 3 (com snapshot de v2), obteve %d", fRollback.Version)
	}
}

func TestBuildWorkerContextWithPublicAPIAndFacts(t *testing.T) {
	bb := NewBlackboard()
	bb.SetManifest(ProjectManifest{
		Name:  "context-test",
		Stack: "Go",
	})
	bb.RecordFact(FactCategoryConfig, "AUTH_METHOD", "JWT_HS256", "Architect")
	bb.RecordLesson("supervisor", "Missing exports", "Certifique-se de exportar structs com inicial maiúscula", "types.go")

	largeGoCode := `package domain

import "context"

type Entity struct {
	ID string
}

func (e *Entity) Process(ctx context.Context) error {
	// simulate 50 lines of internal logic
	return nil
}
`
	for i := 0; i < 40; i++ {
		largeGoCode += "// padding line\n"
	}

	bb.RecordFile("domain/entity.go", "Domain Model", largeGoCode, "Worker-1")

	bb.RegisterTask(TaskNode{
		ID:          "task_1",
		Title:       "Domain",
		TargetFiles: []string{"domain/entity.go"},
		Status:      StatusCompleted,
	})

	workerTask := TaskNode{
		ID:          "task_2",
		Title:       "Service",
		TargetFiles: []string{"service/entity_service.go"},
		DependsOn:   []string{"task_1"},
	}
	bb.RegisterTask(workerTask)

	ctxPrompt := bb.BuildWorkerContext(&workerTask)

	if !strings.Contains(ctxPrompt, "AUTH_METHOD: JWT_HS256") && !strings.Contains(ctxPrompt, "auth_method: JWT_HS256") {
		t.Errorf("WorkerContext não contém os fatos compartilhados")
	}
	if !strings.Contains(ctxPrompt, "LIÇÕES APRENDIDAS - ERROS A EVITAR") {
		t.Errorf("WorkerContext não contém a seção de lições aprendidas")
	}
	if !strings.Contains(ctxPrompt, "Interface Pública & Contratos") {
		t.Errorf("WorkerContext deveria ter injetado a Interface Pública enxuta para o arquivo de dependência")
	}
}
