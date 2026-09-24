package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorktreeManager_Workflow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não encontrado no PATH, pulando teste de worktrees")
	}

	tmpDir := t.TempDir()

	wm, err := NewWorktreeManager(tmpDir)
	if err != nil {
		t.Fatalf("Falha ao criar WorktreeManager: %v", err)
	}
	defer wm.TeardownAll()

	// 1. Create worktree
	wt, err := wm.CreateWorktree("task-auth")
	if err != nil {
		t.Fatalf("Falha ao criar worktree para task-auth: %v", err)
	}

	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("Pasta da worktree %s não foi criada", wt.Path)
	}

	// 2. Write file inside worktree
	testFile := filepath.Join(wt.Path, "auth.txt")
	if err := os.WriteFile(testFile, []byte("auth token"), 0644); err != nil {
		t.Fatalf("Falha ao escrever na worktree: %v", err)
	}

	// 3. Commit in worktree
	if err := wm.CommitWorktree(wt, "feat: add auth"); err != nil {
		t.Fatalf("Falha ao commitar na worktree: %v", err)
	}

	// 4. Merge into main
	if err := wm.MergeWorktree(wt); err != nil {
		t.Fatalf("Falha ao fazer merge da worktree: %v", err)
	}

	// Verify merged file exists in baseDir
	mergedFile := filepath.Join(tmpDir, "auth.txt")
	if _, err := os.Stat(mergedFile); os.IsNotExist(err) {
		t.Errorf("Arquivo %s não foi encontrado na pasta base após merge", mergedFile)
	}

	// 5. Cleanup
	if err := wm.CleanupWorktree(wt); err != nil {
		t.Fatalf("Falha ao limpar worktree: %v", err)
	}

	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("Pasta da worktree %s deveria ter sido removida", wt.Path)
	}
}
