package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"overclock/pkg/client"
	"overclock/pkg/memory"
	"overclock/pkg/ui"
)

var codeBlockRegex = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_-]+)?\\s*(?:(?:#|//)\\s*(?:file:|path:)?\\s*([a-zA-Z0-9_./\\-]+)\\s*)?\\n(.*?)\\n```")

// HandoffSignal represents an event-driven completion payload from an autonomous worker to the Maestro.
type HandoffSignal struct {
	TaskID         string        `json:"task_id"`
	WorkerID       int           `json:"worker_id"`
	Account        string        `json:"account"`
	Stage          int           `json:"stage"`
	TargetFiles    []string      `json:"target_files"`
	GeneratedPaths []string      `json:"generated_paths"`
	Duration       time.Duration `json:"duration"`
	Passed         bool          `json:"passed"`
	Notes          string        `json:"notes"`
	Err            error         `json:"error,omitempty"`
	Worktree       *Worktree     `json:"worktree,omitempty"`
}

// DAGOptions configures the execution of the project DAG.
type DAGOptions struct {
	Concurrency  int
	Accounts     []string
	Model        string
	SystemPrompt string
	UseWorktrees bool
}

// RunDAG executes the project DAG in stages using the concurrent worker pool.
// Maintained for backward compatibility.
func RunDAG(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	outDir string,
	concurrency int,
	accounts []string,
	model string,
	systemPrompt string,
	term *ui.Terminal,
) error {
	opts := DAGOptions{
		Concurrency:  concurrency,
		Accounts:     accounts,
		Model:        model,
		SystemPrompt: systemPrompt,
		UseWorktrees: false,
	}
	return RunDAGWithOptions(ctx, runner, bb, outDir, opts, term)
}

// RunDAGWithOptions executes the DAG using Event-Driven Handoff, Validation Gates, and Auto-Teardown.
func RunDAGWithOptions(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	outDir string,
	opts DAGOptions,
	term *ui.Terminal,
) error {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}

	tasks := bb.GetAllTasks()
	totalTasks := len(tasks)
	term.LogInfo("⚡ OVERCLOCK MAESTRO: Execução orientada a eventos (%d tarefas | %d workers max)", totalTasks, opts.Concurrency)

	var wm *WorktreeManager
	if opts.UseWorktrees && outDir != "" {
		var err error
		wm, err = NewWorktreeManager(outDir)
		if err != nil {
			term.LogWarn("⚠️ Não foi possível inicializar isolamento via Git Worktrees: %v. Operando em modo direto.", err)
			wm = nil
		} else {
			term.LogInfo("🌳 [Git Worktrees] Isolamento ativado: cada worker operará em branch/worktree dedicada")
			defer wm.TeardownAll()
		}
	}

	completedCount := 0
	for _, t := range tasks {
		if t.Status == memory.StatusCompleted {
			completedCount++
		}
	}

	if completedCount >= totalTasks {
		term.LogInfo("🎉 Todas as %d tarefas já estão concluídas!", totalTasks)
		return nil
	}

	overallStart := time.Now()
	sem := make(chan struct{}, opts.Concurrency)
	handoffChan := make(chan HandoffSignal, opts.Concurrency*2)

	activeWorkerID := 0
	var mu sync.Mutex
	runningCount := 0
	completedStages := make(map[int]bool)
	stageWorktrees := make(map[int][]*Worktree)

	// Helper to schedule tasks whose dependencies and Stage Gates are satisfied
	scheduleTasks := func() {
		mu.Lock()
		defer mu.Unlock()

		ready := bb.GetReadyTasks()
		for _, task := range ready {
			if runningCount >= opts.Concurrency {
				break
			}

			// Gate check: do not schedule task if any prior stage is incomplete or not approved
			if task.Stage > 1 {
				priorApproved := true
				for s := 1; s < task.Stage; s++ {
					if !completedStages[s] && stageHasTasks(bb, s) {
						priorApproved = false
						break
					}
				}
				if !priorApproved {
					continue
				}
			}

			// Mark running
			bb.UpdateTaskStatus(task.ID, memory.StatusRunning, 0, "")
			activeWorkerID++
			wID := activeWorkerID
			runningCount++

			go func(t *memory.TaskNode, workerIndex int) {
				// Ephemeral Lifecycle: acquire resource, release automatically at end
				sem <- struct{}{}
				defer func() {
					<-sem
					term.LogInfo("[Worker %d] 🛑 Auto-teardown: Recursos e contexto liberados após handoff (%s)",
						workerIndex, t.Title)
				}()

				account := "default"
				if len(opts.Accounts) > 0 {
					account = opts.Accounts[workerIndex%len(opts.Accounts)]
				}
				t.AssignedWorker = workerIndex
				t.AssignedAcct = account

				term.LogInfo("[Worker %d] [%s] 🚀 Iniciando tarefa: %s (%s)",
					workerIndex, account, t.Title, strings.Join(t.TargetFiles, ", "))

				taskStart := time.Now()
				// Worker scoped timeout to prevent zombie instances
				workerCtx, workerCancel := context.WithTimeout(ctx, 3*time.Minute)
				defer workerCancel()

				generatedPaths, wt, err := executeTaskNodeWithWorktree(workerCtx, runner, bb, t, outDir, opts.Model, opts.SystemPrompt, wm, term)
				taskDur := time.Since(taskStart)

				sig := HandoffSignal{
					TaskID:         t.ID,
					WorkerID:       workerIndex,
					Account:        account,
					Stage:          t.Stage,
					TargetFiles:    t.TargetFiles,
					GeneratedPaths: generatedPaths,
					Duration:       taskDur,
					Passed:         err == nil,
					Err:            err,
					Worktree:       wt,
					Notes:          fmt.Sprintf("Entrega da tarefa %s em %.2fs", t.ID, taskDur.Seconds()),
				}

				// Emit event-driven handoff signal to awaken the Maestro
				handoffChan <- sig
			}(task, wID)
		}
	}

	// Initial dispatch
	scheduleTasks()

	// Event-driven reactive loop (ZERO polling loop)
	for completedCount < totalTasks {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case sig := <-handoffChan:
			// REACTIVE WAKEUP: Maestro woke up instantly upon receiving the handoff event!
			mu.Lock()
			runningCount--
			mu.Unlock()

			if sig.Err != nil {
				term.LogError("[Worker %d] ❌ Falha na tarefa '%s': %v", sig.WorkerID, sig.TaskID, sig.Err)
				bb.UpdateTaskStatus(sig.TaskID, memory.StatusFailed, sig.Duration, sig.Err.Error())
				_ = memory.AppendEvent(outDir, memory.EventHandoffEmitted, sig)
				if outDir != "" {
					_ = bb.SaveState(outDir)
				}
				return fmt.Errorf("falha crítica na tarefa %s: %w", sig.TaskID, sig.Err)
			}

			// Task succeeded
			bb.UpdateTaskStatus(sig.TaskID, memory.StatusCompleted, sig.Duration, "")
			completedCount++
			_ = memory.AppendEvent(outDir, memory.EventHandoffEmitted, sig)
			if outDir != "" {
				_ = bb.SaveState(outDir)
			}

			term.LogInfo("[HANDOFF] 📥 Worker %d entregou '%s' (%d/%d prontas em %.2fs) | Artefatos: %s",
				sig.WorkerID, sig.TaskID, completedCount, totalTasks, sig.Duration.Seconds(), strings.Join(sig.GeneratedPaths, ", "))

			if sig.Worktree != nil {
				stageWorktrees[sig.Stage] = append(stageWorktrees[sig.Stage], sig.Worktree)
			}

			// Check if stage is completed
			if isStageFinished(bb, sig.Stage) && !completedStages[sig.Stage] {
				term.LogInfo("🚪 [Validation Gate 2] Validando critérios de entrega do Estágio %d...", sig.Stage)
				gateEval := EvaluateStageGate(bb, sig.Stage)
				_ = memory.AppendEvent(outDir, memory.EventGateEvaluated, gateEval)

				if !gateEval.Passed {
					term.LogWarn("⚠️ [GATE 2 WARNING] Estágio %d com pendências. Disparando Auto-Recuperação Cirúrgica...", sig.Stage)
					healedEval, healErr := SelfHealStageGate(ctx, runner, bb, sig.Stage, outDir, opts.Model, opts.SystemPrompt, term)
					if healErr != nil {
						term.LogWarn("Aviso durante auto-recuperação: %v", healErr)
					}
					if healedEval != nil {
						gateEval = healedEval
					}
				}

				_ = memory.AppendEvent(outDir, memory.EventGateEvaluated, gateEval)

				if !gateEval.Passed {
					term.LogError("❌ [GATE 2 RED] Estágio %d reprovado mesmo após auto-recuperação: %s", sig.Stage, strings.Join(gateEval.Errors, "; "))
					return fmt.Errorf("portão de validação do estágio %d reprovado: %s", sig.Stage, strings.Join(gateEval.Errors, "; "))
				}

				completedStages[sig.Stage] = true
				term.LogInfo("✅ [GATE 2 GREEN] %s", gateEval.Summary)

				// If worktrees active, merge all worktrees for this stage in order
				if wm != nil && len(stageWorktrees[sig.Stage]) > 0 {
					for _, wt := range stageWorktrees[sig.Stage] {
						term.LogInfo("🔀 [Git Worktree] Integrando branch '%s' via merge controlado...", wt.Branch)
						if mergeErr := wm.MergeWorktree(wt); mergeErr != nil {
							term.LogWarn("Aviso ao fazer merge da worktree %s: %v", wt.Branch, mergeErr)
						} else {
							_ = memory.AppendEvent(outDir, memory.EventWorktreeMerged, wt)
						}
						_ = wm.CleanupWorktree(wt)
					}
				}
			}

			// Deadlock auto-recovery if needed
			mu.Lock()
			if runningCount == 0 && completedCount < totalTasks && len(bb.GetReadyTasks()) == 0 {
				var candidate *memory.TaskNode
				for _, t := range bb.GetAllTasks() {
					if t.Status == memory.StatusPending {
						if candidate == nil || t.Stage < candidate.Stage {
							candidate = t
						}
					}
				}
				if candidate != nil {
					term.LogWarn("[DAG Auto-Recovery] ⚡ Desbloqueando tarefa '%s' (%s) para resolver deadlock",
						candidate.ID, candidate.Title)
					candidate.DependsOn = nil
				}
			}
			mu.Unlock()

			// Dispatch next wave of tasks unlocked by this handoff
			scheduleTasks()
		}
	}

	overallDur := time.Since(overallStart)
	term.LogInfo("⚡ TODAS AS %d TAREFAS CONCLUÍDAS COM SUCESSO EM %.2fs!", totalTasks, overallDur.Seconds())
	return nil
}

func stageHasTasks(bb *memory.Blackboard, stage int) bool {
	for _, t := range bb.GetAllTasks() {
		if t.Stage == stage {
			return true
		}
	}
	return false
}

func isStageFinished(bb *memory.Blackboard, stage int) bool {
	for _, t := range bb.GetAllTasks() {
		if t.Stage == stage && t.Status != memory.StatusCompleted {
			return false
		}
	}
	return true
}

func executeTaskNodeWithWorktree(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	task *memory.TaskNode,
	outDir string,
	model string,
	systemPrompt string,
	wm *WorktreeManager,
	term *ui.Terminal,
) ([]string, *Worktree, error) {
	var wt *Worktree
	targetDir := outDir

	if wm != nil {
		var err error
		wt, err = wm.CreateWorktree(task.ID)
		if err != nil {
			term.LogWarn("Falha ao criar worktree para tarefa %s (%v), usando diretório base", task.ID, err)
		} else {
			targetDir = wt.Path
		}
	}

	generatedPaths, err := executeTaskNode(ctx, runner, bb, task, targetDir, model, systemPrompt, term)
	if err != nil {
		return nil, wt, err
	}

	if wt != nil && wm != nil {
		if commitErr := wm.CommitWorktree(wt, fmt.Sprintf("feat(%s): %s", task.ID, task.Title)); commitErr != nil {
			term.LogWarn("Falha ao commitar na worktree %s: %v", wt.Branch, commitErr)
		}
	}

	return generatedPaths, wt, nil
}

func executeTaskNode(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	task *memory.TaskNode,
	outDir string,
	model string,
	systemPrompt string,
	term *ui.Terminal,
) ([]string, error) {
	workerContext := bb.BuildWorkerContext(task)

	sys := `Você é um Engenheiro de Software Sênior especializado no Overclock Engine.
Sua missão é gerar os arquivos de código completos, modulares, limpos e 100% funcionais solicitados nesta tarefa.

DIRETRIZES DE EXECUÇÃO:
1. Respeite com precisão absoluta os CONTRATOS GLOBAIS, FATOS COMPARTILHADOS e os ARQUIVOS DE DEPENDÊNCIA fornecidos no contexto.
2. Cada arquivo solicitado DEVE ser gerado em um bloco de código markdown delimitado por sua respectiva linguagem.
3. Coloque na PRIMEIRA linha dentro do bloco de código o caminho relativo exato do arquivo no formato:
   // file: caminho/do/arquivo.ext   (ou # file: para python/yaml/bash)
4. NÃO use reticências, NÃO use placeholders tipo "// TODO", gere a implementação COMPLETA e REAL do código.
5. CONVENÇÃO DE IMPORTS E MÓDULOS:
   - Em projetos TypeScript/JavaScript com ECMAScript Modules (ESM / NodeNext, com "type": "module" no package.json ou moduleResolution: "NodeNext" no tsconfig.json), utilize SEMPRE a extensão .js nos imports relativos (ex: import ... from './outro.js').
   - Em projetos TypeScript com bundler (Vite/Webpack) ou CommonJS, utilize imports sem extensão.
   - Em Python, utilize imports relativos coerentes com a estrutura de pacotes.
   - Em Go, respeite o nome do módulo declarado no go.mod.
   - Em Rust, respeite a visibilidade e sistema de módulos (mod/use/crate).
6. Se definir portas, endpoints, convenções de importação ou dependências críticas que os próximos workers precisem saber, registre no final no formato:
   [FACT:CONFIG:PORT] 8080
   [FACT:CONVENTION:IMPORTS] usar extensão .js em imports relativos
   [FACT:DEP:NOME] versão ou instrução
7. Não responda com comandos de terminal. Responda apenas com os blocos de código e breves explicações técnicas.`

	if systemPrompt != "" {
		sys = sys + "\n\n" + systemPrompt
	}

	var pb strings.Builder
	pb.WriteString(workerContext)
	pb.WriteString("\n[SUA TAREFA ATUAL]\n")
	pb.WriteString(fmt.Sprintf("ID: %s\nTítulo: %s\nEstágio: %d\n", task.ID, task.Title, task.Stage))
	pb.WriteString(fmt.Sprintf("Arquivos que você DEVE gerar agora: %s\n\n", strings.Join(task.TargetFiles, ", ")))
	pb.WriteString("ESPECIFICAÇÃO DA TAREFA:\n")
	pb.WriteString(task.Spec)
	pb.WriteString("\n\nGere todos os arquivos acima completos agora.")

	res, err := runner.Execute(ctx, client.RequestOptions{
		Model:        model,
		SystemPrompt: sys,
		Prompt:       pb.String(),
		Stream:       false,
	})
	if err != nil {
		return nil, err
	}

	// Hub-and-Spoke Clean Context Sanitization:
	// Discard internal reasoning monologues and raw conversational fluff before registering artifacts
	sanitized := SanitizeWorkerDelivery(res.Text, task.TargetFiles)
	if term != nil && sanitized.MonologuesSize > 0 {
		term.LogInfo("[Hub-and-Spoke] 🧹 Clean Context: %d bytes de monólogos internos/ruído descartados para a tarefa %s",
			sanitized.MonologuesSize, task.ID)
	}

	// Record discovered shared facts into Blackboard
	for _, df := range sanitized.Facts {
		bb.RecordFact(df.Category, df.Key, df.Value, fmt.Sprintf("Worker-%d", task.AssignedWorker))
	}

	// Register clean files into Blackboard
	var generatedPaths []string
	if len(sanitized.Files) == 0 {
		if len(task.TargetFiles) == 1 {
			target := task.TargetFiles[0]
			clean := stripCodeFences(res.Text)
			bb.RecordFile(target, task.Title, clean, fmt.Sprintf("Worker-%d", task.AssignedWorker))
			generatedPaths = append(generatedPaths, target)
		} else {
			return nil, fmt.Errorf("nenhum arquivo extraído da resposta do worker para a tarefa %s", task.ID)
		}
	} else {
		for path, content := range sanitized.Files {
			bb.RecordFile(path, task.Title, content, fmt.Sprintf("Worker-%d", task.AssignedWorker))
			generatedPaths = append(generatedPaths, path)
		}
	}

	// Write files incrementally
	if outDir != "" && len(generatedPaths) > 0 {
		_ = MaterializeFiles(bb, generatedPaths, outDir)
	}

	return generatedPaths, nil
}

var factDirectiveRegex = regexp.MustCompile(`(?i)\[FACT:(CONFIG|CONVENTION|DEP|RUNTIME|GENERAL):([A-Za-z0-9_.-]+)\]\s*(.+)`)

type extractedFact struct {
	Category memory.FactCategory
	Key      string
	Value    string
}

func extractFactsFromResponse(text string) []extractedFact {
	var facts []extractedFact
	matches := factDirectiveRegex.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) > 3 {
			cat := memory.FactCategory(strings.ToUpper(strings.TrimSpace(m[1])))
			key := strings.TrimSpace(m[2])
			val := strings.TrimSpace(m[3])
			facts = append(facts, extractedFact{
				Category: cat,
				Key:      key,
				Value:    val,
			})
		}
	}
	return facts
}

func extractFilesFromResponse(text string, expectedFiles []string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(text, "\n")

	var currentPath string
	var currentBlock []string
	inBlock := false

	fileHeaderRegex := regexp.MustCompile(`^(?://|#|--|/\*)\s*(?:file:|path:)?\s*([a-zA-Z0-9_.\-\/]+\.[a-zA-Z0-9]+)`)
	markdownHeaderPathRegex := regexp.MustCompile(`###\s+(?:` + "`" + `|\()?([a-zA-Z0-9_.\-\/]+\.[a-zA-Z0-9]+)`)

	lastMarkdownHeader := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "###") || strings.HasPrefix(trimmed, "##") {
			if m := markdownHeaderPathRegex.FindStringSubmatch(trimmed); len(m) > 1 {
				lastMarkdownHeader = m[1]
			}
			if inBlock && (strings.Contains(trimmed, "Explica") || strings.Contains(trimmed, "Tecnic") || strings.Contains(trimmed, "Summary") || strings.Contains(trimmed, "Resumo")) {
				inBlock = false
				if len(currentBlock) > 0 && currentPath != "" {
					result[cleanFilePath(currentPath)] = strings.Join(currentBlock, "\n")
				}
				currentPath = ""
				currentBlock = nil
				continue
			}
		}

		if strings.HasPrefix(trimmed, "```") {
			if !inBlock {
				inBlock = true
				currentBlock = nil
				currentPath = lastMarkdownHeader
			} else {
				inBlock = false
				if len(currentBlock) > 0 && currentPath != "" {
					result[cleanFilePath(currentPath)] = strings.Join(currentBlock, "\n")
				}
				currentPath = ""
				currentBlock = nil
			}
			continue
		}

		if m := fileHeaderRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			if !inBlock {
				inBlock = true
				currentPath = m[1]
				currentBlock = nil
				continue
			} else if len(currentBlock) == 0 {
				currentPath = m[1]
				continue
			}
		}

		if inBlock {
			currentBlock = append(currentBlock, line)
		}
	}

	if inBlock && len(currentBlock) > 0 && currentPath != "" {
		result[cleanFilePath(currentPath)] = strings.Join(currentBlock, "\n")
	}

	if len(result) == 0 && len(expectedFiles) == 1 && len(currentBlock) > 0 {
		firstLine := strings.TrimSpace(currentBlock[0])
		if !strings.HasPrefix(firstLine, "### Explica") && !strings.HasPrefix(firstLine, "### Summary") && !strings.HasPrefix(firstLine, "1. **") {
			result[expectedFiles[0]] = strings.Join(currentBlock, "\n")
		}
	}

	normalized := make(map[string]string)
	for path, content := range result {
		targetKey := cleanFilePath(path)
		for _, exp := range expectedFiles {
			cleanExp := cleanFilePath(exp)
			if targetKey == cleanExp || filepath.Base(targetKey) == filepath.Base(cleanExp) {
				targetKey = cleanExp
				break
			}
		}
		normalized[targetKey] = content
	}

	return normalized
}

func cleanFilePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "`")
	p = strings.TrimSuffix(p, ")")
	return p
}

func stripCodeFences(raw string) string {
	raw = strings.TrimSpace(raw)
	fencedRegex := regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_-]+)?\\s*\\n(.*?)\\n```")
	if m := fencedRegex.FindStringSubmatch(raw); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}

	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
