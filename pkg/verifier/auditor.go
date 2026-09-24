package verifier

import (
	"fmt"
	"path/filepath"
	"strings"

	"overclock/pkg/memory"
)

// AuditReport summarizes the validation of the project blueprint.
type AuditReport struct {
	Valid              bool     `json:"valid"`
	MaxConcurrency     int      `json:"max_concurrency"`
	TotalTasks         int      `json:"total_tasks"`
	TotalFiles         int      `json:"total_files"`
	Warnings           []string `json:"warnings"`
	Errors             []string `json:"errors"`
	AutoPatchesApplied []string `json:"auto_patches_applied"`
}

// AuditBlueprint audits the planned project blueprint inside the Blackboard.
// It detects and fixes missing critical manifests, circular dependencies, forward dependencies,
// and guarantees that at least one task is immediately runnable without deadlocks.
func AuditBlueprint(bb *memory.Blackboard) (*AuditReport, error) {
	manifest := bb.GetManifest()
	tasks := bb.GetAllTasks()

	report := &AuditReport{
		Valid:              true,
		TotalTasks:         len(tasks),
		Warnings:           make([]string, 0),
		Errors:             make([]string, 0),
		AutoPatchesApplied: make([]string, 0),
	}

	if len(tasks) == 0 {
		report.Valid = false
		report.Errors = append(report.Errors, "Nenhuma tarefa foi gerada pelo Arquiteto.")
		return report, fmt.Errorf("plano vazio: nenhuma tarefa gerada")
	}

	// 1. Map all files to tasks, and build task lookup
	taskMap := make(map[string]*memory.TaskNode)
	fileToTask := make(map[string]string)
	fileSet := make(map[string]bool)
	stageCounts := make(map[int]int)

	for _, t := range tasks {
		taskMap[t.ID] = t
		stageCounts[t.Stage]++
		for _, f := range t.TargetFiles {
			fileSet[f] = true
			fileToTask[f] = t.ID
		}
	}
	report.TotalFiles = len(fileSet)

	// Max concurrency calculation
	maxStage := 1
	for _, count := range stageCounts {
		if count > maxStage {
			maxStage = count
		}
	}
	report.MaxConcurrency = maxStage

	// 2. Normalize and Sanitize Dependencies for every task
	for _, t := range tasks {
		// Stage 1 tasks are foundation: they must NEVER depend on anything!
		if t.Stage <= 1 {
			if len(t.DependsOn) > 0 {
				report.AutoPatchesApplied = append(report.AutoPatchesApplied,
					fmt.Sprintf("Removidas dependências da tarefa de fundação '%s' (estágio 1 deve ser autônomo)", t.ID))
				t.DependsOn = nil
			}
			continue
		}

		var validDeps []string
		seen := make(map[string]bool)

		for _, rawDep := range t.DependsOn {
			depID := strings.TrimSpace(rawDep)

			// If the dependency was written as a file path (e.g. "package.json"), resolve to its producer task
			if producerID, isFile := fileToTask[depID]; isFile {
				depID = producerID
			}

			// Self-dependency check
			if depID == t.ID {
				report.Warnings = append(report.Warnings, fmt.Sprintf("Tarefa '%s' dependia de si mesma (ciclo removido)", t.ID))
				continue
			}

			depTask, exists := taskMap[depID]
			if !exists {
				report.AutoPatchesApplied = append(report.AutoPatchesApplied,
					fmt.Sprintf("Removida dependência inexistente '%s' da tarefa '%s'", depID, t.ID))
				continue
			}

			// Forward dependency check: task in Stage 2 cannot depend on Stage 3 or 4
			if depTask.Stage >= t.Stage {
				report.AutoPatchesApplied = append(report.AutoPatchesApplied,
					fmt.Sprintf("Removida dependência invertida '%s' (estágio %d) da tarefa '%s' (estágio %d)",
						depID, depTask.Stage, t.ID, t.Stage))
				continue
			}

			if !seen[depID] {
				seen[depID] = true
				validDeps = append(validDeps, depID)
			}
		}

		t.DependsOn = validDeps
	}

	// 3. Cycle Detection & Automatic Cycle Breaking (Topological Sort / Kahn's algorithm)
	inDegree := make(map[string]int)
	adj := make(map[string][]string)

	for _, t := range tasks {
		inDegree[t.ID] = len(t.DependsOn)
		for _, depID := range t.DependsOn {
			adj[depID] = append(adj[depID], t.ID)
		}
	}

	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visitedCount := 0
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		visitedCount++

		for _, neighbor := range adj[curr] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	// If not all tasks were visited, a cycle exists among the remaining tasks
	if visitedCount < len(tasks) {
		report.Warnings = append(report.Warnings, "Ciclo detectado no grafo de dependências. Desfazendo ciclo automaticamente...")
		for id, deg := range inDegree {
			if deg > 0 {
				if t, ok := taskMap[id]; ok {
					report.AutoPatchesApplied = append(report.AutoPatchesApplied,
						fmt.Sprintf("Ciclo quebrado: removidas dependências da tarefa '%s'", t.ID))
					t.DependsOn = nil
				}
			}
		}
	}

	// 4. Guarantee at least one Ready Task at start
	readyTasks := bb.GetReadyTasks()
	if len(readyTasks) == 0 && len(tasks) > 0 {
		// Unlock lowest stage task
		lowest := tasks[0]
		for _, t := range tasks {
			if t.Stage < lowest.Stage {
				lowest = t
			}
		}
		lowest.DependsOn = nil
		lowest.Stage = 1
		report.AutoPatchesApplied = append(report.AutoPatchesApplied,
			fmt.Sprintf("Garantida inicialização imediata: tarefa '%s' definida como raiz sem dependências", lowest.ID))
	}

	// 5. Stack-specific critical file verification
	stackLower := strings.ToLower(manifest.Stack)
	pkgManager := strings.ToLower(manifest.PackageManager)

	if strings.Contains(stackLower, "go") || pkgManager == "go" {
		if !fileSet["go.mod"] {
			report.AutoPatchesApplied = append(report.AutoPatchesApplied, "Adicionado 'go.mod' à Tarefa de Fundação")
			addFileToFirstStageTask(tasks, "go.mod")
			fileSet["go.mod"] = true
			report.TotalFiles++
		}
	} else if strings.Contains(stackLower, "react") || strings.Contains(stackLower, "vite") ||
		strings.Contains(stackLower, "node") || strings.Contains(stackLower, "next") ||
		pkgManager == "bun" || pkgManager == "pnpm" || pkgManager == "npm" {
		if !fileSet["package.json"] {
			report.AutoPatchesApplied = append(report.AutoPatchesApplied, "Adicionado 'package.json' à Tarefa de Fundação")
			addFileToFirstStageTask(tasks, "package.json")
			fileSet["package.json"] = true
			report.TotalFiles++
		}
	} else if strings.Contains(stackLower, "python") || pkgManager == "pip" || pkgManager == "uv" || pkgManager == "poetry" {
		if !fileSet["requirements.txt"] && !fileSet["pyproject.toml"] {
			report.AutoPatchesApplied = append(report.AutoPatchesApplied, "Adicionado 'pyproject.toml' à Tarefa de Fundação")
			addFileToFirstStageTask(tasks, "pyproject.toml")
			fileSet["pyproject.toml"] = true
			report.TotalFiles++
		}
	} else if strings.Contains(stackLower, "rust") || pkgManager == "cargo" {
		if !fileSet["Cargo.toml"] {
			report.AutoPatchesApplied = append(report.AutoPatchesApplied, "Adicionado 'Cargo.toml' à Tarefa de Fundação")
			addFileToFirstStageTask(tasks, "Cargo.toml")
			fileSet["Cargo.toml"] = true
			report.TotalFiles++
		}
	}

	// 6. Ensure README.md is scheduled
	if !fileSet["README.md"] {
		addFileToLastStageTask(tasks, "README.md")
		report.AutoPatchesApplied = append(report.AutoPatchesApplied, "Adicionado 'README.md' à Tarefa de Finalização")
		fileSet["README.md"] = true
		report.TotalFiles++
	}

	// 7. Ensure Test Suite Coverage for Core Modules (Stage 2/3)
	ensureTestSuiteCoverage(bb, tasks, report, fileSet)

	return report, nil
}

func addFileToFirstStageTask(tasks []*memory.TaskNode, filename string) {
	for _, t := range tasks {
		if t.Stage == 1 {
			t.TargetFiles = append([]string{filename}, t.TargetFiles...)
			return
		}
	}
	if len(tasks) > 0 {
		tasks[0].TargetFiles = append([]string{filename}, tasks[0].TargetFiles...)
	}
}

func addFileToLastStageTask(tasks []*memory.TaskNode, filename string) {
	var lastTask *memory.TaskNode
	highestStage := -1

	for _, t := range tasks {
		if t.Stage > highestStage {
			highestStage = t.Stage
			lastTask = t
		}
	}
	if lastTask != nil {
		lastTask.TargetFiles = append(lastTask.TargetFiles, filename)
	} else if len(tasks) > 0 {
		tasks[len(tasks)-1].TargetFiles = append(tasks[len(tasks)-1].TargetFiles, filename)
	}
}

func isTestFilePath(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "_test.go") ||
		strings.Contains(lower, ".test.ts") ||
		strings.Contains(lower, ".spec.ts") ||
		strings.Contains(lower, ".test.js") ||
		strings.Contains(lower, ".spec.js") ||
		strings.Contains(lower, "_test.py") ||
		strings.HasPrefix(filepath.Base(lower), "test_") ||
		strings.Contains(lower, "/tests/") ||
		strings.HasSuffix(lower, "_test.rs")
}

func ensureTestSuiteCoverage(
	bb *memory.Blackboard,
	tasks []*memory.TaskNode,
	report *AuditReport,
	fileSet map[string]bool,
) {
	// Check if test files are already scheduled anywhere in the blueprint
	for f := range fileSet {
		if isTestFilePath(f) {
			return
		}
	}

	manifest := bb.GetManifest()
	stackLower := strings.ToLower(manifest.Stack)

	// Identify candidate domain/core files from Stage 2 (or Stage 1/3)
	var candidates []string
	var producerIDs []string
	for _, t := range tasks {
		if t.Stage == 2 || (t.Stage == 3 && len(candidates) == 0) {
			for _, f := range t.TargetFiles {
				base := strings.ToLower(filepath.Base(f))
				if strings.Contains(base, "main") || strings.Contains(base, "mod") ||
					strings.Contains(base, "config") || strings.Contains(base, "readme") ||
					strings.Contains(base, "package.json") {
					continue
				}
				candidates = append(candidates, f)
				producerIDs = append(producerIDs, t.ID)
			}
		}
	}

	var testFiles []string
	switch {
	case strings.Contains(stackLower, "go"):
		if len(candidates) > 0 {
			for i, c := range candidates {
				if i >= 2 {
					break
				}
				ext := filepath.Ext(c)
				if ext == ".go" {
					testFiles = append(testFiles, strings.TrimSuffix(c, ".go")+"_test.go")
				}
			}
		}
		if len(testFiles) == 0 {
			testFiles = []string{"pkg/core_test.go"}
		}

	case strings.Contains(stackLower, "react") || strings.Contains(stackLower, "node") ||
		strings.Contains(stackLower, "vite") || strings.Contains(stackLower, "ts") ||
		strings.Contains(stackLower, "typescript"):
		if len(candidates) > 0 {
			for i, c := range candidates {
				if i >= 2 {
					break
				}
				ext := filepath.Ext(c)
				if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
					testFiles = append(testFiles, strings.TrimSuffix(c, ext)+".test"+ext)
				}
			}
		}
		if len(testFiles) == 0 {
			testFiles = []string{"src/app.test.ts"}
		}

	case strings.Contains(stackLower, "python"):
		if len(candidates) > 0 {
			for i, c := range candidates {
				if i >= 2 {
					break
				}
				dir := filepath.Dir(c)
				base := filepath.Base(c)
				testFiles = append(testFiles, filepath.Join(dir, "test_"+base))
			}
		}
		if len(testFiles) == 0 {
			testFiles = []string{"tests/test_main.py"}
		}

	case strings.Contains(stackLower, "rust"):
		testFiles = []string{"tests/unit_test.rs"}

	default:
		testFiles = []string{"tests/unit_test.txt"}
	}

	// Determine final stage
	highestStage := 1
	for _, t := range tasks {
		if t.Stage > highestStage {
			highestStage = t.Stage
		}
	}

	testTask := memory.TaskNode{
		ID:          fmt.Sprintf("task_tests_auto_%d", len(tasks)+1),
		Title:       "Implementação da Suíte de Testes Automatizados",
		Stage:       highestStage,
		TargetFiles: testFiles,
		DependsOn:   producerIDs,
		Spec: fmt.Sprintf("Implemente a suíte completa de testes unitários automatizados cobrindo os módulos: %s. Valide cenários de sucesso, limites, erros e concorrência.",
			strings.Join(testFiles, ", ")),
		Status: memory.StatusPending,
	}

	bb.RegisterTask(testTask)
	for _, tf := range testFiles {
		fileSet[tf] = true
	}
	report.TotalFiles += len(testFiles)
	report.TotalTasks++
	report.AutoPatchesApplied = append(report.AutoPatchesApplied,
		fmt.Sprintf("Adicionada tarefa obrigatória de testes unitários (%s) para: %s", testTask.ID, strings.Join(testFiles, ", ")))
}
