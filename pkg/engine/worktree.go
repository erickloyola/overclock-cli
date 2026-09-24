package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Worktree represents an isolated Git worktree allocation for a specific worker task.
type Worktree struct {
	TaskID     string `json:"task_id"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	BaseBranch string `json:"base_branch"`
}

// WorktreeManager governs concurrent Git worktree isolation, merging, and hermetic teardown.
type WorktreeManager struct {
	baseDir    string
	baseBranch string
	mu         sync.Mutex
	active     map[string]*Worktree
}

// NewWorktreeManager initializes a WorktreeManager for the target project directory.
func NewWorktreeManager(baseDir string) (*WorktreeManager, error) {
	absDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("caminho inválido para worktrees: %w", err)
	}

	if err := os.MkdirAll(absDir, 0755); err != nil {
		return nil, fmt.Errorf("falha ao criar pasta base para git: %w", err)
	}

	wm := &WorktreeManager{
		baseDir:    absDir,
		baseBranch: "main",
		active:     make(map[string]*Worktree),
	}

	if err := wm.ensureGitRepo(); err != nil {
		return nil, err
	}

	return wm, nil
}

// ensureGitRepo checks or initializes git in the base directory with an initial commit.
func (wm *WorktreeManager) ensureGitRepo() error {
	gitDir := filepath.Join(wm.baseDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmdInit := exec.Command("git", "init", "-b", wm.baseBranch)
		cmdInit.Dir = wm.baseDir
		if out, err := cmdInit.CombinedOutput(); err != nil {
			// Older git may not support -b flag
			cmdInitOld := exec.Command("git", "init")
			cmdInitOld.Dir = wm.baseDir
			if _, errOld := cmdInitOld.CombinedOutput(); errOld != nil {
				return fmt.Errorf("falha ao inicializar git init em %s: %s", wm.baseDir, string(out))
			}
		}

		_ = exec.Command("git", "-C", wm.baseDir, "config", "user.name", "Overclock Maestro").Run()
		_ = exec.Command("git", "-C", wm.baseDir, "config", "user.email", "maestro@overclock.local").Run()

		// Create initial empty commit so branches can be formed
		keepPath := filepath.Join(wm.baseDir, ".gitkeep")
		_ = os.WriteFile(keepPath, []byte("# Overclock Workspace\n"), 0644)
		_ = exec.Command("git", "-C", wm.baseDir, "add", ".gitkeep").Run()
		_ = exec.Command("git", "-C", wm.baseDir, "commit", "-m", "chore: initial workspace commit").Run()
	}

	return nil
}

// CreateWorktree provisions an isolated Git worktree for a specific task.
func (wm *WorktreeManager) CreateWorktree(taskID string) (*Worktree, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	cleanID := strings.ReplaceAll(taskID, " ", "-")
	cleanID = strings.ReplaceAll(cleanID, "/", "-")
	branch := fmt.Sprintf("feat/%s", cleanID)
	wtDir := filepath.Join(wm.baseDir, ".worktrees", cleanID)

	if err := os.MkdirAll(filepath.Dir(wtDir), 0755); err != nil {
		return nil, fmt.Errorf("falha ao criar pasta .worktrees: %w", err)
	}

	// Remove any leftover worktree path before adding
	_ = exec.Command("git", "-C", wm.baseDir, "worktree", "remove", "--force", wtDir).Run()
	_ = exec.Command("git", "-C", wm.baseDir, "branch", "-D", branch).Run()

	cmd := exec.Command("git", "-C", wm.baseDir, "worktree", "add", "-b", branch, wtDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("falha ao criar git worktree para %s: %s (%w)", taskID, string(out), err)
	}

	wt := &Worktree{
		TaskID:     taskID,
		Branch:     branch,
		Path:       wtDir,
		BaseBranch: wm.baseBranch,
	}

	wm.active[taskID] = wt
	return wt, nil
}

// CommitWorktree stages and commits generated files inside the hermetic worktree.
func (wm *WorktreeManager) CommitWorktree(wt *Worktree, message string) error {
	cmdAdd := exec.Command("git", "-C", wt.Path, "add", "-A")
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		return fmt.Errorf("falha no git add dentro da worktree %s: %s", wt.Path, string(out))
	}

	cmdCommit := exec.Command("git", "-C", wt.Path, "commit", "-m", message)
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		// If nothing to commit, return nil
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("falha no git commit dentro da worktree %s: %s", wt.Path, string(out))
	}

	return nil
}

// MergeWorktree merges the task's worktree branch into the base integration branch with --no-ff.
func (wm *WorktreeManager) MergeWorktree(wt *Worktree) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	cmdMerge := exec.Command("git", "-C", wm.baseDir, "merge", "--no-ff", wt.Branch,
		"-m", fmt.Sprintf("feat: merge task %s from worktree", wt.TaskID))
	if out, err := cmdMerge.CombinedOutput(); err != nil {
		return fmt.Errorf("falha no merge da worktree branch %s: %s", wt.Branch, string(out))
	}

	return nil
}

// CleanupWorktree removes the physical worktree folder and prunes its git references.
func (wm *WorktreeManager) CleanupWorktree(wt *Worktree) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	cmdRemove := exec.Command("git", "-C", wm.baseDir, "worktree", "remove", "--force", wt.Path)
	_ = cmdRemove.Run()

	cmdPrune := exec.Command("git", "-C", wm.baseDir, "worktree", "prune")
	_ = cmdPrune.Run()

	cmdBranch := exec.Command("git", "-C", wm.baseDir, "branch", "-d", wt.Branch)
	_ = cmdBranch.Run()

	delete(wm.active, wt.TaskID)
	return nil
}

// TeardownAll forcefully cleans up any remaining worktrees.
func (wm *WorktreeManager) TeardownAll() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	for _, wt := range wm.active {
		_ = exec.Command("git", "-C", wm.baseDir, "worktree", "remove", "--force", wt.Path).Run()
		_ = exec.Command("git", "-C", wm.baseDir, "branch", "-D", wt.Branch).Run()
	}
	_ = exec.Command("git", "-C", wm.baseDir, "worktree", "prune").Run()
	wm.active = make(map[string]*Worktree)
}
