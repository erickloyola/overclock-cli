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

// RunDAG executes the project DAG in stages using the concurrent worker pool.
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
	if concurrency <= 0 {
		concurrency = 4
	}

	tasks := bb.GetAllTasks()
	totalTasks := len(tasks)
	term.LogInfo("⚡ OVERCLOCK DAG: Iniciando execução de %d tarefas com concorrência máxima de %d workers", totalTasks, concurrency)

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var globalErr error

	overallStart := time.Now()
	completedCount := 0
	for _, t := range tasks {
		if t.Status == memory.StatusCompleted {
			completedCount++
		}
	}
	activeWorkerID := 0
	var mu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		readyTasks := bb.GetReadyTasks()
		if len(readyTasks) == 0 {
			// Check if all are done
			allDone := true
			for _, t := range bb.GetAllTasks() {
				if t.Status != memory.StatusCompleted {
					allDone = false
					break
				}
			}
			if allDone {
				break
			}

			// If no tasks ready but some still running or pending, sleep briefly
			time.Sleep(100 * time.Millisecond)

			// Check if deadlocked (no running tasks, but pending remain)
			anyRunning := false
			for _, t := range bb.GetAllTasks() {
				if t.Status == memory.StatusRunning {
					anyRunning = true
					break
				}
			}
			if !anyRunning && len(bb.GetReadyTasks()) == 0 {
				// Dynamic self-healing: find pending tasks and unlock the one with lowest stage
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
					continue
				}

				// If all non-completed tasks are in Failed state, stop
				break
			}
		}

		for _, task := range readyTasks {
			bb.UpdateTaskStatus(task.ID, memory.StatusRunning, 0, "")

			mu.Lock()
			activeWorkerID++
			wID := activeWorkerID
			mu.Unlock()

			wg.Add(1)
			go func(t *memory.TaskNode, workerIndex int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				account := "default"
				if len(accounts) > 0 {
					account = accounts[workerIndex%len(accounts)]
				}
				t.AssignedWorker = workerIndex
				t.AssignedAcct = account

				term.LogInfo("[Worker %d] [%s] 🚀 Iniciando: %s (%s)",
					workerIndex, account, t.Title, strings.Join(t.TargetFiles, ", "))

				taskStart := time.Now()
				err := executeTaskNode(ctx, runner, bb, t, outDir, model, systemPrompt)
				taskDur := time.Since(taskStart)

				if err != nil {
					term.LogError("[Worker %d] ❌ Falha na tarefa '%s': %v", workerIndex, t.Title, err)
					bb.UpdateTaskStatus(t.ID, memory.StatusFailed, taskDur, err.Error())
					if outDir != "" {
						_ = bb.SaveState(outDir)
					}
					errOnce.Do(func() {
						globalErr = fmt.Errorf("falha crítica na tarefa %s: %w", t.ID, err)
					})
				} else {
					bb.UpdateTaskStatus(t.ID, memory.StatusCompleted, taskDur, "")
					if outDir != "" {
						_ = bb.SaveState(outDir)
					}
					mu.Lock()
					completedCount++
					term.LogInfo("[Worker %d] ✅ Concluído: %s (%d/%d tarefas prontas em %.2fs)",
						workerIndex, t.Title, completedCount, totalTasks, taskDur.Seconds())
					mu.Unlock()
				}
			}(task, wID)
		}
	}

	wg.Wait()
	overallDur := time.Since(overallStart)

	if globalErr != nil {
		return globalErr
	}

	term.LogInfo("⚡ TODAS AS %d TAREFAS CONCLUÍDAS COM SUCESSO EM %.2fs!", totalTasks, overallDur.Seconds())
	return nil
}

func executeTaskNode(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	task *memory.TaskNode,
	outDir string,
	model string,
	systemPrompt string,
) error {
	workerContext := bb.BuildWorkerContext(task)

	sys := `Você é um Engenheiro de Software Sênior especializado no Overclock Engine.
Sua missão é gerar os arquivos de código completos, modulares, limpos e 100% funcionais solicitados nesta tarefa.

DIRETRIZES DE EXECUÇÃO:
1. Respeite com precisão absoluta os CONTRATOS GLOBAIS, FATOS COMPARTILHADOS e os ARQUIVOS DE DEPENDÊNCIA fornecidos no contexto.
2. Cada arquivo solicitado DEVE ser gerado em um bloco de código markdown delimitado por sua respectiva linguagem.
3. Coloque na PRIMEIRA linha dentro do bloco de código o caminho relativo exato do arquivo no formato:
   // file: caminho/do/arquivo.ext   (ou # file: para python/yaml/bash)
4. NÃO use reticências, NÃO use placeholders tipo "// TODO", gere a implementação COMPLETA e REAL do código.
5. Se definir portas, endpoints, convenções ou dependências críticas que os próximos workers precisem saber, registre no final no formato:
   [FACT:CONFIG:PORT] 8080
   [FACT:DEP:NOME] versão ou instrução
6. Não responda com comandos de terminal. Responda apenas com os blocos de código e breves explicações técnicas.`

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
		return err
	}

	// Extract and record any shared facts declared by worker into shared memory (OverMemory)
	discoveredFacts := extractFactsFromResponse(res.Text)
	for _, df := range discoveredFacts {
		bb.RecordFact(df.Category, df.Key, df.Value, fmt.Sprintf("Worker-%d", task.AssignedWorker))
	}

	// Parse generated code blocks
	var generatedPaths []string
	extracted := extractFilesFromResponse(res.Text, task.TargetFiles)
	if len(extracted) == 0 {
		// Fallback: If single target file, use the entire code block or text
		if len(task.TargetFiles) == 1 {
			target := task.TargetFiles[0]
			clean := stripCodeFences(res.Text)
			bb.RecordFile(target, task.Title, clean, fmt.Sprintf("Worker-%d", task.AssignedWorker))
			generatedPaths = append(generatedPaths, target)
		} else {
			return fmt.Errorf("nenhum arquivo extraído da resposta do worker para a tarefa %s", task.ID)
		}
	} else {
		for path, content := range extracted {
			bb.RecordFile(path, task.Title, content, fmt.Sprintf("Worker-%d", task.AssignedWorker))
			generatedPaths = append(generatedPaths, path)
		}
	}

	// Incremental write: save generated files immediately to disk
	if outDir != "" && len(generatedPaths) > 0 {
		_ = MaterializeFiles(bb, generatedPaths, outDir)
	}

	return nil
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

		// Check for markdown headers like ### src/core/state.ts
		if strings.HasPrefix(trimmed, "###") || strings.HasPrefix(trimmed, "##") {
			if m := markdownHeaderPathRegex.FindStringSubmatch(trimmed); len(m) > 1 {
				lastMarkdownHeader = m[1]
			}
			// If we are currently collecting an un-fenced code block and hit an explanation header, close it
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

		// Handle opening / closing code fences
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

		// Detect file header comment (e.g., // file: src/core/state.ts)
		if m := fileHeaderRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			// If not in a block, the AI started writing code directly without ```
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

	// If reached EOF while still inBlock (e.g. un-fenced code without trailing ```)
	if inBlock && len(currentBlock) > 0 && currentPath != "" {
		result[cleanFilePath(currentPath)] = strings.Join(currentBlock, "\n")
	}

	// Match expected file if single target file and only 1 block was extracted without a path header
	if len(result) == 0 && len(expectedFiles) == 1 && len(currentBlock) > 0 {
		// Filter out pure markdown explanation
		firstLine := strings.TrimSpace(currentBlock[0])
		if !strings.HasPrefix(firstLine, "### Explica") && !strings.HasPrefix(firstLine, "### Summary") && !strings.HasPrefix(firstLine, "1. **") {
			result[expectedFiles[0]] = strings.Join(currentBlock, "\n")
		}
	}

	// Normalize extracted paths against expectedFiles (e.g., if worker omitted parent directories)
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
	// Try extracting from fenced block first
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
