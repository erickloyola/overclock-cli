package engine

import (
	"context"
	"testing"

	"overclock/pkg/client"
	"overclock/pkg/memory"
)

func TestEvaluateContractSpecGate(t *testing.T) {
	bb := memory.NewBlackboard()

	// Empty blackboard should fail Gate 1
	evalEmpty := EvaluateContractSpecGate(bb)
	if evalEmpty.Passed {
		t.Errorf("Esperava Gate 1 reprovar para blackboard vazio, mas passou")
	}

	// Populated manifest and valid Stage 1 task
	bb.SetManifest(memory.ProjectManifest{
		Name:           "TestProject",
		Stack:          "Go",
		PackageManager: "go",
	})
	bb.SetContract("IUser", "type IUser interface { ID() string }")
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_1",
		Title:       "Setup Module",
		Stage:       1,
		TargetFiles: []string{"go.mod"},
		Status:      memory.StatusPending,
	})

	evalValid := EvaluateContractSpecGate(bb)
	if !evalValid.Passed {
		t.Errorf("Esperava Gate 1 aprovar com manifesto e tarefas válidas, mas falhou: %v", evalValid.Errors)
	}
}

func TestEvaluateStageGate(t *testing.T) {
	bb := memory.NewBlackboard()
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_1",
		Title:       "Types",
		Stage:       1,
		TargetFiles: []string{"types.go"},
		Status:      memory.StatusRunning, // Not completed yet
	})

	evalRunning := EvaluateStageGate(bb, 1)
	if evalRunning.Passed {
		t.Errorf("Gate 2 não deveria aprovar estágio com tarefa em execução")
	}

	// Mark completed without file in blackboard -> should fail
	bb.UpdateTaskStatus("task_1", memory.StatusCompleted, 0, "")
	evalNoFile := EvaluateStageGate(bb, 1)
	if evalNoFile.Passed {
		t.Errorf("Gate 2 não deveria aprovar quando arquivo alvo não existe no blackboard")
	}

	// Record valid file with matching braces
	bb.RecordFile("types.go", "Types", "package main\n\ntype User struct {\n\tID string\n}\n", "worker-1")
	evalPass := EvaluateStageGate(bb, 1)
	if !evalPass.Passed {
		t.Errorf("Gate 2 deveria aprovar quando todos os artefatos do estágio estão válidos: %v", evalPass.Errors)
	}
}

type mockHealRunner struct{}

func (m *mockHealRunner) Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error) {
	return &client.ExecutionResult{
		Text: "```go\n// file: missing.go\npackage main\n\nfunc Recovered() bool { return true }\n```\n",
	}, nil
}

func (m *mockHealRunner) Name() string { return "MockHealRunner" }

func TestSelfHealStageGate(t *testing.T) {
	bb := memory.NewBlackboard()
	bb.RegisterTask(memory.TaskNode{
		ID:          "task_defect",
		Title:       "Defective Task",
		Stage:       1,
		TargetFiles: []string{"missing.go"},
		Status:      memory.StatusCompleted,
	})

	// Initial evaluation must fail because missing.go is not in Blackboard
	initialEval := EvaluateStageGate(bb, 1)
	if initialEval.Passed {
		t.Fatalf("Esperava Gate 2 reprovar com arquivo ausente")
	}

	defects := FindStageDefects(bb, 1)
	if len(defects) != 1 || defects[0].FilePath != "missing.go" {
		t.Fatalf("Esperava 1 defeito para 'missing.go', obteve: %+v", defects)
	}

	// Run SelfHealStageGate with mock runner
	healedEval, err := SelfHealStageGate(context.Background(), &mockHealRunner{}, bb, 1, t.TempDir(), "model", "", nil)
	if err != nil {
		t.Fatalf("SelfHealStageGate falhou com erro: %v", err)
	}

	if !healedEval.Passed {
		t.Fatalf("Esperava Gate 2 aprovado após auto-recuperação cirúrgica, mas falhou: %v", healedEval.Errors)
	}

	fileArt, exists := bb.GetFile("missing.go")
	if !exists || fileArt == nil {
		t.Fatalf("Esperava que 'missing.go' estivesse gravado no Blackboard após auto-recuperação")
	}

	if fileArt.GeneratedBy != "Gate2-SelfHeal" {
		t.Errorf("Esperava GeneratedBy == 'Gate2-SelfHeal', obteve: %s", fileArt.GeneratedBy)
	}
}
