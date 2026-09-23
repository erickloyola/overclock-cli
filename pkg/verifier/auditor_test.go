package verifier

import (
	"testing"

	"overclock/pkg/memory"
)

func TestAuditBlueprintAutoPatch(t *testing.T) {
	bb := memory.NewBlackboard()
	bb.SetManifest(memory.ProjectManifest{
		Name:           "go-api",
		Stack:          "Go + Gin",
		PackageManager: "go",
	})

	// Register task missing go.mod and README.md
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_1",
		Title:       "Models",
		Stage:       1,
		TargetFiles: []string{"models/user.go"},
		DependsOn:   []string{"task_missing"}, // orphan dep
	})

	report, err := AuditBlueprint(bb)
	if err != nil {
		t.Fatalf("AuditBlueprint retornou erro: %v", err)
	}

	if !report.Valid {
		t.Fatalf("Esperava plano válido após auto-patch")
	}

	// Check if go.mod was auto-added
	task, _ := bb.GetTask("task_1")
	hasGoMod := false
	for _, f := range task.TargetFiles {
		if f == "go.mod" {
			hasGoMod = true
		}
	}
	if !hasGoMod {
		t.Errorf("Esperava que 'go.mod' fosse adicionado automaticamente")
	}

	// Check if orphan dep was removed
	if len(task.DependsOn) != 0 {
		t.Errorf("Esperava que dependência órfã fosse removida, obteve: %v", task.DependsOn)
	}
}

func TestAuditBlueprintCycleBreakingAndDeadlockPrevention(t *testing.T) {
	bb := memory.NewBlackboard()
	bb.SetManifest(memory.ProjectManifest{
		Name:           "node-app",
		Stack:          "React + Vite",
		PackageManager: "bun",
	})

	// Create a nasty mutual cycle: task_a depends on task_b, task_b depends on task_a
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_a",
		Title:       "Task A",
		Stage:       2,
		TargetFiles: []string{"src/a.ts"},
		DependsOn:   []string{"task_b"},
	})

	bb.RegisterTask(memory.TaskNode{
		ID:          "task_b",
		Title:       "Task B",
		Stage:       2,
		TargetFiles: []string{"src/b.ts"},
		DependsOn:   []string{"task_a"},
	})

	report, err := AuditBlueprint(bb)
	if err != nil {
		t.Fatalf("AuditBlueprint falhou: %v", err)
	}

	if !report.Valid {
		t.Errorf("Esperava relatório válido após auto-correção de ciclo")
	}

	// Guarantee that at least one task is ready immediately
	ready := bb.GetReadyTasks()
	if len(ready) == 0 {
		t.Errorf("Esperava que pelo menos uma tarefa ficasse desbloqueada para evitar deadlock")
	}
}
