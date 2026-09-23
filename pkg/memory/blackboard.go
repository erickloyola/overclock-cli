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

// FactCategory classifies a shared piece of operational knowledge (OverMemory style).
type FactCategory string

const (
	FactCategoryConfig     FactCategory = "CONFIG"     // Ports, hostnames, paths, environment variables
	FactCategoryConvention FactCategory = "CONVENTION" // Naming conventions, architectural rules, code style
	FactCategoryDependency FactCategory = "DEP"        // Package versions, required tooling, caveats
	FactCategoryRuntime    FactCategory = "RUNTIME"    // Build flags, execution commands, runtime requirements
	FactCategoryGeneral    FactCategory = "GENERAL"    // Generic operational notes
)

// SharedFact represents a durable piece of operational knowledge across all workers.
type SharedFact struct {
	ID        string       `json:"id"`
	Category  FactCategory `json:"category"`
	Key       string       `json:"key"`
	Value     string       `json:"value"`
	Source    string       `json:"source"`
	CreatedAt time.Time    `json:"created_at"`
}

// LessonLearned captures a compilation or QA error and its mitigation guidance (Negative Feedback Memory).
type LessonLearned struct {
	ID        string    `json:"id"`
	Trigger   string    `json:"trigger"`  // "compiler", "supervisor", "runtime"
	Pattern   string    `json:"pattern"`  // What went wrong (error description or signature)
	Guidance  string    `json:"guidance"` // Actionable mitigation instruction
	File      string    `json:"file,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// FileRevision holds an immutable snapshot of a prior version of a file.
type FileRevision struct {
	Version    int       `json:"version"`
	Content    string    `json:"content"`
	Bytes      int       `json:"bytes"`
	Hash       string    `json:"hash"`
	ModifiedBy string    `json:"modified_by"`
	Reason     string    `json:"reason,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// FileArtifact represents a generated code or configuration file.
type FileArtifact struct {
	Path        string         `json:"path"`
	Purpose     string         `json:"purpose"`
	Content     string         `json:"content"`
	PublicAPI   string         `json:"public_api,omitempty"` // Compact API surface / interface stub
	Hash        string         `json:"hash"`
	Bytes       int            `json:"bytes"`
	Exports     []string       `json:"exports,omitempty"`
	Imports     []string       `json:"imports,omitempty"`
	GeneratedBy string         `json:"generated_by"`
	Version     int            `json:"version"`
	History     []FileRevision `json:"history,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
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
	Facts       map[string]*SharedFact   `json:"facts"`   // key -> SharedFact (OverMemory)
	Lessons     []LessonLearned          `json:"lessons"` // Compiler/supervisor lessons learned
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
		Facts:       make(map[string]*SharedFact),
		Lessons:     make([]LessonLearned, 0),
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
// It computes hashing and public API extraction BEFORE acquiring the write lock, avoiding mutex contention.
func (b *Blackboard) RecordFile(path, purpose, content, generatedBy string) {
	h := sha256.Sum256([]byte(content))
	hashStr := hex.EncodeToString(h[:])
	publicAPI := ExtractPublicAPI(path, content)

	b.mu.Lock()
	defer b.mu.Unlock()

	existing, exists := b.Files[path]
	version := 1
	var history []FileRevision

	if exists && existing != nil {
		version = existing.Version + 1
		history = make([]FileRevision, len(existing.History), len(existing.History)+1)
		copy(history, existing.History)

		// Archive current state into revision history
		history = append(history, FileRevision{
			Version:    existing.Version,
			Content:    existing.Content,
			Bytes:      existing.Bytes,
			Hash:       existing.Hash,
			ModifiedBy: existing.GeneratedBy,
			Reason:     purpose,
			Timestamp:  existing.UpdatedAt,
		})
	}

	b.Files[path] = &FileArtifact{
		Path:        path,
		Purpose:     purpose,
		Content:     content,
		PublicAPI:   publicAPI,
		Hash:        hashStr,
		Bytes:       len(content),
		GeneratedBy: generatedBy,
		Version:     version,
		History:     history,
		UpdatedAt:   time.Now(),
	}
}

// GetFileRevisions returns all past revisions of a given file.
func (b *Blackboard) GetFileRevisions(path string) []FileRevision {
	b.mu.RLock()
	defer b.mu.RUnlock()

	f, exists := b.Files[path]
	if !exists || f == nil {
		return nil
	}
	cp := make([]FileRevision, len(f.History))
	copy(cp, f.History)
	return cp
}

// RollbackFile restores a file to a specific prior version in its revision history.
func (b *Blackboard) RollbackFile(path string, targetVersion int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	f, exists := b.Files[path]
	if !exists || f == nil {
		return fmt.Errorf("arquivo %s não encontrado para rollback", path)
	}

	for _, rev := range f.History {
		if rev.Version == targetVersion {
			newHistory := append(f.History, FileRevision{
				Version:    f.Version,
				Content:    f.Content,
				Bytes:      f.Bytes,
				Hash:       f.Hash,
				ModifiedBy: f.GeneratedBy,
				Reason:     fmt.Sprintf("Rollback para v%d", targetVersion),
				Timestamp:  time.Now(),
			})

			f.Version++
			f.Content = rev.Content
			f.Bytes = rev.Bytes
			f.Hash = rev.Hash
			f.GeneratedBy = fmt.Sprintf("Rollback(v%d)", targetVersion)
			f.PublicAPI = ExtractPublicAPI(path, rev.Content)
			f.History = newHistory
			f.UpdatedAt = time.Now()
			return nil
		}
	}

	return fmt.Errorf("versão %d não encontrada no histórico do arquivo %s", targetVersion, path)
}

// RecordFact stores or updates an operational fact in shared memory (OverMemory style).
func (b *Blackboard) RecordFact(category FactCategory, key, value, source string) *SharedFact {
	b.mu.Lock()
	defer b.mu.Unlock()

	normKey := strings.ToLower(strings.TrimSpace(key))
	fact := &SharedFact{
		ID:        fmt.Sprintf("fact_%d", len(b.Facts)+1),
		Category:  category,
		Key:       normKey,
		Value:     strings.TrimSpace(value),
		Source:    source,
		CreatedAt: time.Now(),
	}
	b.Facts[normKey] = fact
	return fact
}

// GetFact retrieves a shared fact by key.
func (b *Blackboard) GetFact(key string) (*SharedFact, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	fact, ok := b.Facts[strings.ToLower(strings.TrimSpace(key))]
	return fact, ok
}

// GetFacts returns a copy of all recorded shared facts.
func (b *Blackboard) GetFacts() []*SharedFact {
	b.mu.RLock()
	defer b.mu.RUnlock()
	list := make([]*SharedFact, 0, len(b.Facts))
	for _, f := range b.Facts {
		list = append(list, f)
	}
	return list
}

// GetFactsByCategory returns all facts matching a given category.
func (b *Blackboard) GetFactsByCategory(category FactCategory) []*SharedFact {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var list []*SharedFact
	for _, f := range b.Facts {
		if f.Category == category {
			list = append(list, f)
		}
	}
	return list
}

// FactsSummary builds a formatted markdown string of all shared facts for worker prompt injection.
func (b *Blackboard) FactsSummary() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.Facts) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[FATOS E DECISÕES COMPARTILHADAS (KNOWLEDGE BASE)]\n")
	sb.WriteString("Todos os workers devem respeitar estas decisões e configurações aprendidas durante a execução:\n")

	for _, f := range b.Facts {
		src := f.Source
		if src == "" {
			src = "Sistema"
		}
		sb.WriteString(fmt.Sprintf("• [%s] %s: %s (registrado por %s)\n", f.Category, f.Key, f.Value, src))
	}
	sb.WriteString("\n")
	return sb.String()
}

// RecordLesson records a defect or compiler error pattern and its actionable mitigation guidance.
func (b *Blackboard) RecordLesson(trigger, pattern, guidance, file string) *LessonLearned {
	b.mu.Lock()
	defer b.mu.Unlock()

	lesson := LessonLearned{
		ID:        fmt.Sprintf("lesson_%d", len(b.Lessons)+1),
		Trigger:   trigger,
		Pattern:   strings.TrimSpace(pattern),
		Guidance:  strings.TrimSpace(guidance),
		File:      file,
		CreatedAt: time.Now(),
	}
	b.Lessons = append(b.Lessons, lesson)
	return &lesson
}

// GetLessons returns all recorded lessons learned.
func (b *Blackboard) GetLessons() []LessonLearned {
	b.mu.RLock()
	defer b.mu.RUnlock()
	cp := make([]LessonLearned, len(b.Lessons))
	copy(cp, b.Lessons)
	return cp
}

// LessonsSummary builds a formatted markdown string of lessons learned from previous failures.
func (b *Blackboard) LessonsSummary() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.Lessons) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[LIÇÕES APRENDIDAS - ERROS A EVITAR]\n")
	sb.WriteString("Atenção especial para NÃO repetir os seguintes erros detectados anteriormente no projeto:\n")

	for _, l := range b.Lessons {
		fileContext := ""
		if l.File != "" {
			fileContext = fmt.Sprintf(" no arquivo %s", l.File)
		}
		sb.WriteString(fmt.Sprintf("• [%s%s] Problema: %s\n  ➔ Ação corretiva: %s\n", l.Trigger, fileContext, l.Pattern, l.Guidance))
	}
	sb.WriteString("\n")
	return sb.String()
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

// MarkNotesFixed marks all supervisor notes for a specific file as fixed.
func (b *Blackboard) MarkNotesFixed(file string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	clean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(file), "./"), "/")
	for i := range b.Notes {
		noteClean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(b.Notes[i].File), "./"), "/")
		if noteClean == clean || b.Notes[i].File == file {
			b.Notes[i].Fixed = true
		}
	}
}

// BuildWorkerContext generates dynamic, tailored context injection for a specific task worker.
// It includes: Manifest + Contracts + Shared Facts + Lessons Learned + Stack Configs + Upstream Dependencies (pruned via PublicAPI).
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

	// 3. Shared Facts / Knowledge Base (OverMemory)
	if len(b.Facts) > 0 {
		sb.WriteString("[FATOS E DECISÕES COMPARTILHADAS (KNOWLEDGE BASE)]\n")
		for _, f := range b.Facts {
			sb.WriteString(fmt.Sprintf("• [%s] %s: %s\n", f.Category, f.Key, f.Value))
		}
		sb.WriteString("\n")
	}

	// 4. Lessons Learned / Erros a Evitar
	if len(b.Lessons) > 0 {
		sb.WriteString("[LIÇÕES APRENDIDAS - ERROS A EVITAR]\n")
		for _, l := range b.Lessons {
			fileCtx := ""
			if l.File != "" {
				fileCtx = fmt.Sprintf(" (%s)", l.File)
			}
			sb.WriteString(fmt.Sprintf("• [%s%s] Evite: %s ➔ Correto: %s\n", l.Trigger, fileCtx, l.Pattern, l.Guidance))
		}
		sb.WriteString("\n")
	}

	// 5. Foundation Configs & Package Manifests (if already generated in Stage 1)
	var foundationConfigs []string
	for _, confName := range []string{"go.mod", "Cargo.toml", "requirements.txt", "package.json", "Makefile", "pyproject.toml", "CMakeLists.txt"} {
		if fArt, ok := b.Files[confName]; ok {
			foundationConfigs = append(foundationConfigs, fmt.Sprintf("--- %s (Manifesto da Stack) ---\n%s\n", confName, strings.TrimSpace(fArt.Content)))
		}
	}
	if len(foundationConfigs) > 0 {
		sb.WriteString("[CONFIGURAÇÃO E DEPENDÊNCIAS DA STACK]\n")
		for _, cfgText := range foundationConfigs {
			sb.WriteString(cfgText)
			sb.WriteString("\n")
		}
	}

	// 6. Upstream Dependency Files (pruned with PublicAPI to prevent context window bloat)
	if len(task.DependsOn) > 0 {
		sb.WriteString("[ARQUIVOS DE DEPENDÊNCIAS JÁ GERADOS]\n")
		for _, depID := range task.DependsOn {
			depTask, exists := b.Tasks[depID]
			if !exists {
				continue
			}
			for _, targetPath := range depTask.TargetFiles {
				if fileArt, ok := b.Files[targetPath]; ok {
					lines := strings.Split(fileArt.Content, "\n")
					// Use PublicAPI stub if available and file is large (> 40 lines) to save 60-80% tokens
					if fileArt.PublicAPI != "" && len(lines) > 40 && len(fileArt.PublicAPI) < len(fileArt.Content) {
						sb.WriteString(fmt.Sprintf("--- %s (Interface Pública & Contratos - gerado pela tarefa %s) ---\n", targetPath, depID))
						sb.WriteString(fileArt.PublicAPI)
						sb.WriteString("\n\n")
					} else if len(lines) <= 1000 {
						sb.WriteString(fmt.Sprintf("--- %s (gerado pela tarefa %s) ---\n", targetPath, depID))
						sb.WriteString(fileArt.Content)
						sb.WriteString("\n\n")
					} else {
						sb.WriteString(fmt.Sprintf("--- %s (gerado pela tarefa %s) ---\n", targetPath, depID))
						sb.WriteString(strings.Join(lines[:800], "\n"))
						sb.WriteString(fmt.Sprintf("\n... [%d linhas intermediárias omitidas] ...\n", len(lines)-800))
						for _, l := range lines[800:] {
							trimmed := strings.TrimSpace(l)
							if strings.HasPrefix(trimmed, "export ") || strings.HasPrefix(trimmed, "type ") ||
								strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
								strings.HasPrefix(trimmed, "class ") || strings.HasPrefix(trimmed, "pub fn ") ||
								strings.HasPrefix(trimmed, "pub struct ") || strings.HasPrefix(trimmed, "pub enum ") ||
								strings.HasPrefix(trimmed, "fn ") || strings.HasPrefix(trimmed, "struct ") {
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

// BlackboardSnapshot is an immutable view of the blackboard state for safe serialization without lock contention.
type BlackboardSnapshot struct {
	Manifest    ProjectManifest          `json:"manifest"`
	Contracts   map[string]string        `json:"contracts"`
	Files       map[string]*FileArtifact `json:"files"`
	Tasks       map[string]*TaskNode     `json:"tasks"`
	Environment map[string]string        `json:"environment"`
	Notes       []SupervisorNote         `json:"notes"`
	Facts       map[string]*SharedFact   `json:"facts"`
	Lessons     []LessonLearned          `json:"lessons"`
	CreatedAt   time.Time                `json:"created_at"`
}

// SaveState serializes the entire blackboard to disk at <outDir>/.overclock/state.json.
// It snapshots map pointers under a brief read lock, then performs JSON encoding and file I/O
// completely lock-free to prevent stalling worker execution in DAG runners.
func (b *Blackboard) SaveState(outDir string) error {
	b.mu.RLock()
	snap := BlackboardSnapshot{
		Manifest:    b.Manifest,
		Contracts:   make(map[string]string, len(b.Contracts)),
		Files:       make(map[string]*FileArtifact, len(b.Files)),
		Tasks:       make(map[string]*TaskNode, len(b.Tasks)),
		Environment: make(map[string]string, len(b.Environment)),
		Notes:       make([]SupervisorNote, len(b.Notes)),
		Facts:       make(map[string]*SharedFact, len(b.Facts)),
		Lessons:     make([]LessonLearned, len(b.Lessons)),
		CreatedAt:   b.CreatedAt,
	}
	for k, v := range b.Contracts {
		snap.Contracts[k] = v
	}
	for k, v := range b.Files {
		snap.Files[k] = v
	}
	for k, v := range b.Tasks {
		snap.Tasks[k] = v
	}
	for k, v := range b.Environment {
		snap.Environment[k] = v
	}
	copy(snap.Notes, b.Notes)
	for k, v := range b.Facts {
		snap.Facts[k] = v
	}
	copy(snap.Lessons, b.Lessons)
	b.mu.RUnlock()

	data, err := json.MarshalIndent(snap, "", "  ")
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
	for _, f := range bb.Files {
		if f != nil && f.Version <= 0 {
			f.Version = 1
		}
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
	if bb.Facts == nil {
		bb.Facts = make(map[string]*SharedFact)
	}
	if bb.Lessons == nil {
		bb.Lessons = make([]LessonLearned, 0)
	}

	return &bb, nil
}
