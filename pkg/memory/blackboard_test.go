package memory

import (
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

