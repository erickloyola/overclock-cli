package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TaskStatus represents the lifecycle state of a task in the DAG.
type TaskStatus string

const (
	StatusPending   TaskStatus = "PENDING"
	StatusReady     TaskStatus = "READY"
	StatusRunning   TaskStatus = "RUNNING"
	StatusCompleted TaskStatus = "COMPLETED"
	StatusFailed    TaskStatus = "FAILED"
)

// ProjectManifest contains technical specifications and architecture decisions for the project.
type ProjectManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Stack          string   `json:"stack"`
	PackageManager string   `json:"package_manager"` // bun, pnpm, npm, go, cargo, pip, etc.
	RunCommand     string   `json:"run_command"`     // e.g. "bun dev", "go run .", "python main.py"
	BuildCommand   string   `json:"build_command"`   // e.g. "bun build", "go build"
	TestCommand    string   `json:"test_command"`    // e.g. "bun test", "go test ./..."
	Conventions    []string `json:"conventions"`    // styling, architecture rules, folder patterns
}

// FileArtifact represents a generated code or configuration file.
type FileArtifact struct {
	Path        string    `json:"path"`
	Purpose     string    `json:"purpose"`
	Content     string    `json:"content"`
	Hash        string    `json:"hash"`
	Bytes       int       `json:"bytes"`
	Exports     []string  `json:"exports,omitempty"`
	Imports     []string  `json:"imports,omitempty"`
	GeneratedBy string    `json:"generated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TaskNode represents a node in the project execution Directed Acyclic Graph (DAG).
type TaskNode struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Stage          int           `json:"stage"` // 1: Foundation/Types, 2: Core/Services, 3: UI/Endpoints, 4: Finalize
	TargetFiles    []string      `json:"target_files"`
	DependsOn      []string      `json:"depends_on"`
	Spec           string        `json:"spec"`
	Status         TaskStatus    `json:"status"`
	AssignedWorker int           `json:"assigned_worker,omitempty"`
	AssignedAcct   string        `json:"assigned_acct,omitempty"`
	Duration       time.Duration `json:"duration,omitempty"`
	Error          string        `json:"error,omitempty"`
}

// SupervisorNote represents an observation or suggested fix from the QA supervisor.
type SupervisorNote struct {
	File        string    `json:"file"`
	Severity    string    `json:"severity"` // "info", "warn", "error"
	Description string    `json:"description"`
	SuggestedBy string    `json:"suggested_by"`
	Fixed       bool      `json:"fixed"`
	Timestamp   time.Time `json:"timestamp"`
}

// Blackboard is the thread-safe in-memory shared state for all multi-agent instances.
type Blackboard struct {
	mu          sync.RWMutex
	Manifest    ProjectManifest          `json:"manifest"`
	Contracts   map[string]string        `json:"contracts"` // "types/index.ts" -> definition code
	Files       map[string]*FileArtifact `json:"files"`     // "src/App.tsx" -> FileArtifact
	Tasks       map[string]*TaskNode     `json:"tasks"`     // "task_1" -> TaskNode
	Environment map[string]string        `json:"environment"`
	Notes       []SupervisorNote         `json:"notes"`
	CreatedAt   time.Time                `json:"created_at"`
}

// NewBlackboard creates an initialized shared blackboard.
func NewBlackboard() *Blackboard {
	return &Blackboard{
		Contracts:   make(map[string]string),
		Files:       make(map[string]*FileArtifact),
		Tasks:       make(map[string]*TaskNode),
		Environment: make(map[string]string),
		Notes:       make([]SupervisorNote, 0),
		CreatedAt:   time.Now(),
	}
}

// SetManifest sets the project manifest.
func (b *Blackboard) SetManifest(manifest ProjectManifest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Manifest = manifest
}

// GetManifest retrieves a copy of the project manifest.
func (b *Blackboard) GetManifest() ProjectManifest {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Manifest
}

// SetContract records a shared contract (interfaces, schemas, types).
func (b *Blackboard) SetContract(name, content string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Contracts[name] = content
}

// GetContracts returns all recorded contracts.
func (b *Blackboard) GetContracts() map[string]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	copyMap := make(map[string]string, len(b.Contracts))
	for k, v := range b.Contracts {
		copyMap[k] = v
	}
	return copyMap
}

// ContractsSummary builds a formatted markdown string of all shared contracts for worker prompt injection.
func (b *Blackboard) ContractsSummary() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.Contracts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[CONTRATOS GLOBAIS COMPARTILHADOS]\n")
	sb.WriteString("Todos os arquivos gerados DEVEM respeitar estritamente estas interfaces e tipos:\n\n")

	for name, content := range b.Contracts {
		sb.WriteString(fmt.Sprintf("=== CONTRATO: %s ===\n%s\n\n", name, strings.TrimSpace(content)))
	}
	return sb.String()
}

// SetEnvironment stores detected environment details (e.g. tools, versions, OS).
func (b *Blackboard) SetEnvironment(key, value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Environment[key] = value
}

// GetEnvironment returns detected environment parameters.
func (b *Blackboard) GetEnvironment() map[string]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	cp := make(map[string]string, len(b.Environment))
	for k, v := range b.Environment {
		cp[k] = v
	}
	return cp
}

// RegisterTask adds a task node into the DAG.
func (b *Blackboard) RegisterTask(task TaskNode) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if task.Status == "" {
		task.Status = StatusPending
	}
	b.Tasks[task.ID] = &task
}

// GetTask returns a task by ID.
func (b *Blackboard) GetTask(id string) (*TaskNode, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	t, ok := b.Tasks[id]
	return t, ok
}

// GetAllTasks returns all tasks sorted by stage.
func (b *Blackboard) GetAllTasks() []*TaskNode {
	b.mu.RLock()
	defer b.mu.RUnlock()

	list := make([]*TaskNode, 0, len(b.Tasks))
	for _, t := range b.Tasks {
		list = append(list, t)
	}
	return list
}

// GetReadyTasks returns tasks whose dependencies are all completed and are currently pending.
func (b *Blackboard) GetReadyTasks() []*TaskNode {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var ready []*TaskNode
	for _, t := range b.Tasks {
		if t.Status != StatusPending {
			continue
		}

		depsSatisfied := true
		for _, depID := range t.DependsOn {
			depTask, exists := b.Tasks[depID]
			if !exists || depTask.Status != StatusCompleted {
				depsSatisfied = false
				break
			}
		}

		if depsSatisfied {
			ready = append(ready, t)
		}
	}
	return ready
}

// UpdateTaskStatus changes the status and optional error of a task.
func (b *Blackboard) UpdateTaskStatus(id string, status TaskStatus, duration time.Duration, err string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if t, ok := b.Tasks[id]; ok {
		t.Status = status
		t.Duration = duration
		t.Error = err
	}
}

// RecordFile records or updates a generated file artifact in shared memory.
func (b *Blackboard) RecordFile(path, purpose, content, generatedBy string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	h := sha256.Sum256([]byte(content))
	hashStr := hex.EncodeToString(h[:])

	b.Files[path] = &FileArtifact{
		Path:        path,
		Purpose:     purpose,
		Content:     content,
		Hash:        hashStr,
		Bytes:       len(content),
		GeneratedBy: generatedBy,
		UpdatedAt:   time.Now(),
	}
}

// GetFile retrieves a file artifact by path.
func (b *Blackboard) GetFile(path string) (*FileArtifact, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	f, ok := b.Files[path]
	return f, ok
}

// GetAllFiles returns all recorded files in the project.
func (b *Blackboard) GetAllFiles() []*FileArtifact {
	b.mu.RLock()
	defer b.mu.RUnlock()

	list := make([]*FileArtifact, 0, len(b.Files))
	for _, f := range b.Files {
		list = append(list, f)
	}
	return list
}

// AddSupervisorNote appends a note/observation from the supervisor.
func (b *Blackboard) AddSupervisorNote(note SupervisorNote) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if note.Timestamp.IsZero() {
		note.Timestamp = time.Now()
	}
	b.Notes = append(b.Notes, note)
}

// GetNotes returns all supervisor notes.
func (b *Blackboard) GetNotes() []SupervisorNote {
	b.mu.RLock()
	defer b.mu.RUnlock()
	cp := make([]SupervisorNote, len(b.Notes))
	copy(cp, b.Notes)
	return cp
}

// BuildWorkerContext generates dynamic, tailored context injection for a specific task worker.
// It includes: Manifest + Contracts + Content of direct dependencies (without flooding the prompt).
func (b *Blackboard) BuildWorkerContext(task *TaskNode) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var sb strings.Builder

	// 1. Project Manifest
	sb.WriteString("[MANIFESTO DO PROJETO]\n")
	sb.WriteString(fmt.Sprintf("Nome: %s\nStack: %s\nGerenciador: %s\nComando Execução: %s\n\n",
		b.Manifest.Name, b.Manifest.Stack, b.Manifest.PackageManager, b.Manifest.RunCommand))

	if len(b.Manifest.Conventions) > 0 {
		sb.WriteString("Convenções do Projeto:\n")
		for _, conv := range b.Manifest.Conventions {
			sb.WriteString(fmt.Sprintf("• %s\n", conv))
		}
		sb.WriteString("\n")
	}

	// 2. Global Contracts
	if len(b.Contracts) > 0 {
		sb.WriteString("[CONTRATOS GLOBAIS DE TIPOS E INTERFACES]\n")
		for name, c := range b.Contracts {
			sb.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", name, strings.TrimSpace(c)))
		}
	}

	// 3. Upstream Dependency Files
	if len(task.DependsOn) > 0 {
		sb.WriteString("[ARQUIVOS DE DEPENDÊNCIAS JÁ GERADOS]\n")
		for _, depID := range task.DependsOn {
			depTask, exists := b.Tasks[depID]
			if !exists {
				continue
			}
			for _, targetPath := range depTask.TargetFiles {
				if fileArt, ok := b.Files[targetPath]; ok {
					sb.WriteString(fmt.Sprintf("--- %s (gerado pela tarefa %s) ---\n", targetPath, depID))
					// Inject full file content up to 1000 lines to ensure all exports and signatures are visible
					lines := strings.Split(fileArt.Content, "\n")
					if len(lines) <= 1000 {
						sb.WriteString(fileArt.Content)
						sb.WriteString("\n\n")
					} else {
						// For exceptionally large files (>1000 lines), preserve first 800 lines and all subsequent exports
						sb.WriteString(strings.Join(lines[:800], "\n"))
						sb.WriteString(fmt.Sprintf("\n... [%d linhas intermediárias omitidas] ...\n", len(lines)-800))
						for _, l := range lines[800:] {
							trimmed := strings.TrimSpace(l)
							if strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "func ") {
								sb.WriteString(l + "\n")
							}
						}
						sb.WriteString("\n\n")
					}
				}
			}
		}
	}

	return sb.String()
}

// SaveState serializes the entire blackboard to disk at <outDir>/.overclock/state.json.
// It uses atomic file replacement (.tmp -> .json) to prevent race conditions or partial writes.
func (b *Blackboard) SaveState(outDir string) error {
	b.mu.RLock()
	data, err := json.MarshalIndent(b, "", "  ")
	b.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("falha ao serializar estado da memória: %w", err)
	}

	stateDir := filepath.Join(outDir, ".overclock")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("falha ao criar pasta .overclock: %w", err)
	}

	stateFile := filepath.Join(stateDir, "state.json")
	tmpFile := filepath.Join(stateDir, fmt.Sprintf("state-%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("falha ao gravar estado temporário: %w", err)
	}

	if err := os.Rename(tmpFile, stateFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("falha ao atualizar state.json atomicamente: %w", err)
	}

	return nil
}

// LoadState loads a previously saved state from disk.
func LoadState(filePath string) (*Blackboard, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var bb Blackboard
	if err := json.Unmarshal(data, &bb); err != nil {
		return nil, err
	}

	if bb.Contracts == nil {
		bb.Contracts = make(map[string]string)
	}
	if bb.Files == nil {
		bb.Files = make(map[string]*FileArtifact)
	}
	if bb.Tasks == nil {
		bb.Tasks = make(map[string]*TaskNode)
	}
	if bb.Environment == nil {
		bb.Environment = make(map[string]string)
	}
	if bb.Notes == nil {
		bb.Notes = make([]SupervisorNote, 0)
	}

	return &bb, nil
}
