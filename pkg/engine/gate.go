package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"overclock/pkg/client"
	"overclock/pkg/memory"
	"overclock/pkg/supervisor"
	"overclock/pkg/ui"
)

// GateType defines the validation barrier category in the development lifecycle.
type GateType string

const (
	// GateContractSpec validates architecture, schema contracts, and the initial DAG blueprint.
	GateContractSpec GateType = "GATE_1_CONTRACT_SPEC"
	// GateStageUnit validates that all tasks in a given stage completed with valid, non-empty, well-formed code.
	GateStageUnit GateType = "GATE_2_STAGE_UNIT"
	// GateIntegrationQA validates cross-file consistency, imports, and lack of critical supervisor errors.
	GateIntegrationQA GateType = "GATE_3_INTEGRATION_QA"
)

// GateEvaluation contains the result of evaluating a validation gate.
type GateEvaluation struct {
	Type        GateType  `json:"type"`
	Stage       int       `json:"stage,omitempty"`
	Passed      bool      `json:"passed"`
	Errors      []string  `json:"errors"`
	Warnings    []string  `json:"warnings"`
	Artifacts   []string  `json:"artifacts"`
	EvaluatedAt time.Time `json:"evaluated_at"`
	Summary     string    `json:"summary"`
}

// EvaluateContractSpecGate audits the project blueprint, schemas, and contracts (Gate 1).
func EvaluateContractSpecGate(bb *memory.Blackboard) *GateEvaluation {
	eval := &GateEvaluation{
		Type:        GateContractSpec,
		Passed:      true,
		Errors:      make([]string, 0),
		Warnings:    make([]string, 0),
		Artifacts:   make([]string, 0),
		EvaluatedAt: time.Now(),
	}

	manifest := bb.GetManifest()
	if strings.TrimSpace(manifest.Name) == "" {
		eval.Errors = append(eval.Errors, "Manifesto do projeto sem nome definido")
	}
	if strings.TrimSpace(manifest.Stack) == "" {
		eval.Errors = append(eval.Errors, "Manifesto do projeto sem stack tecnológica definida")
	}

	tasks := bb.GetAllTasks()
	if len(tasks) == 0 {
		eval.Errors = append(eval.Errors, "Nenhuma tarefa definida no plano do Arquiteto")
	}

	contracts := bb.GetContracts()
	for name := range contracts {
		eval.Artifacts = append(eval.Artifacts, "contract:"+name)
	}

	hasStage1 := false
	for _, t := range tasks {
		if t.Stage <= 1 {
			hasStage1 = true
		}
		if len(t.TargetFiles) == 0 {
			eval.Warnings = append(eval.Warnings, fmt.Sprintf("Tarefa '%s' não possui arquivos alvo (TargetFiles) explícitos", t.ID))
		}
	}

	if !hasStage1 && len(tasks) > 0 {
		eval.Errors = append(eval.Errors, "Nenhuma tarefa de fundação (Estágio 1) identificada no DAG")
	}

	if len(eval.Errors) > 0 {
		eval.Passed = false
		eval.Summary = fmt.Sprintf("Gate 1 REPROVADO com %d erro(s)", len(eval.Errors))
	} else {
		eval.Summary = fmt.Sprintf("Gate 1 APROVADO: %d tarefas e %d contratos validados", len(tasks), len(contracts))
	}

	return eval
}

// EvaluateStageGate audits that all tasks belonging to a specific stage have completed
// and produced non-empty, well-formed code before instantiating the next stage (Gate 2).
func EvaluateStageGate(bb *memory.Blackboard, stage int) *GateEvaluation {
	eval := &GateEvaluation{
		Type:        GateStageUnit,
		Stage:       stage,
		Passed:      true,
		Errors:      make([]string, 0),
		Warnings:    make([]string, 0),
		Artifacts:   make([]string, 0),
		EvaluatedAt: time.Now(),
	}

	tasks := bb.GetAllTasks()
	stageTasks := make([]*memory.TaskNode, 0)
	for _, t := range tasks {
		if t.Stage == stage {
			stageTasks = append(stageTasks, t)
		}
	}

	if len(stageTasks) == 0 {
		eval.Summary = fmt.Sprintf("Gate 2 Estágio %d: Nenhuma tarefa neste estágio", stage)
		return eval
	}

	for _, t := range stageTasks {
		if t.Status != memory.StatusCompleted {
			eval.Errors = append(eval.Errors, fmt.Sprintf("Tarefa '%s' (%s) não está completada (Status atual: %s)", t.ID, t.Title, t.Status))
			continue
		}

		for _, target := range t.TargetFiles {
			clean := cleanFilePath(target)
			fileArt, ok := bb.GetFile(clean)
			if !ok {
				fileArt, ok = bb.GetFile(target)
			}

			if !ok || fileArt == nil {
				eval.Errors = append(eval.Errors, fmt.Sprintf("Arquivo alvo '%s' da tarefa '%s' não foi registrado no Blackboard", target, t.ID))
				continue
			}

			eval.Artifacts = append(eval.Artifacts, clean)

			if len(strings.TrimSpace(fileArt.Content)) == 0 {
				eval.Errors = append(eval.Errors, fmt.Sprintf("Arquivo '%s' gerado está vazio (0 bytes)", clean))
				continue
			}

			// Structural checks: verify basic block closure (curly braces balance if C-style/Go/JS)
			if strings.HasSuffix(clean, ".go") || strings.HasSuffix(clean, ".ts") || strings.HasSuffix(clean, ".js") || strings.HasSuffix(clean, ".rs") {
				openCount := strings.Count(fileArt.Content, "{")
				closeCount := strings.Count(fileArt.Content, "}")
				if openCount != closeCount {
					eval.Warnings = append(eval.Warnings, fmt.Sprintf("Possível erro de sintaxe em '%s': contagem de chaves divergente ({: %d, }: %d)", clean, openCount, closeCount))
				}
			}

			// Check for unfulfilled placeholders
			if strings.Contains(fileArt.Content, "TODO: implement") || strings.Contains(fileArt.Content, "// TODO implement") {
				eval.Warnings = append(eval.Warnings, fmt.Sprintf("Arquivo '%s' contém comentários de placeholder não implementados", clean))
			}
		}
	}

	if len(eval.Errors) > 0 {
		eval.Passed = false
		eval.Summary = fmt.Sprintf("Gate 2 Estágio %d REPROVADO: %d erro(s) em %d artefatos", stage, len(eval.Errors), len(eval.Artifacts))
	} else {
		eval.Summary = fmt.Sprintf("Gate 2 Estágio %d APROVADO: %d artefatos validados com sucesso", stage, len(eval.Artifacts))
	}

	return eval
}

// StageDefect identifies a specific missing, empty, or defective file artifact in a stage.
type StageDefect struct {
	TaskID    string
	FilePath  string
	Reason    string
	IsMissing bool
	IsEmpty   bool
}

// FindStageDefects locates missing or 0-byte file artifacts for all tasks belonging to a stage.
func FindStageDefects(bb *memory.Blackboard, stage int) []StageDefect {
	var defects []StageDefect
	tasks := bb.GetAllTasks()

	for _, t := range tasks {
		if t.Stage != stage {
			continue
		}

		for _, target := range t.TargetFiles {
			clean := cleanFilePath(target)
			fileArt, ok := bb.GetFile(clean)
			if !ok {
				fileArt, ok = bb.GetFile(target)
			}

			if !ok || fileArt == nil {
				defects = append(defects, StageDefect{
					TaskID:    t.ID,
					FilePath:  target,
					Reason:    fmt.Sprintf("Arquivo '%s' não foi registrado no Blackboard pela tarefa '%s'", target, t.ID),
					IsMissing: true,
				})
				continue
			}

			if len(strings.TrimSpace(fileArt.Content)) == 0 {
				defects = append(defects, StageDefect{
					TaskID:    t.ID,
					FilePath:  clean,
					Reason:    fmt.Sprintf("Arquivo '%s' gerado está vazio (0 bytes)", clean),
					IsEmpty:   true,
				})
			}
		}
	}

	return defects
}

// SelfHealStageGate attempts surgical re-generation of missing or defective files in a stage
// before failing Gate 2. It gives up to 2 attempts per defective artifact.
func SelfHealStageGate(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	stage int,
	outDir string,
	model string,
	systemPrompt string,
	term *ui.Terminal,
) (*GateEvaluation, error) {
	eval := EvaluateStageGate(bb, stage)
	if eval.Passed {
		return eval, nil
	}

	defects := FindStageDefects(bb, stage)
	if len(defects) == 0 {
		return eval, nil
	}

	maxAttempts := 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if term != nil {
			term.LogWarn("🩹 [Gate 2 Self-Healing] Tentativa %d/%d: auto-recuperação cirúrgica de %d artefato(s) no Estágio %d...",
				attempt, maxAttempts, len(defects), stage)
		}

		for _, d := range defects {
			task, hasTask := bb.GetTask(d.TaskID)
			if !hasTask || task == nil {
				continue
			}

			if term != nil {
				term.LogInfo("🔧 [Gate 2 Self-Healing] Gerando arquivo ausente '%s' da tarefa '%s' (%s)...",
					d.FilePath, d.TaskID, task.Title)
			}

			workerContext := bb.BuildWorkerContext(task)

			sys := `Você é o Especialista de Auto-Recuperação Cirúrgica do Overclock.
Sua missão é gerar EXCLUSIVAMENTE o arquivo solicitado abaixo que faltou ou veio vazio na entrega da tarefa.
DIRETRIZES:
1. Respeite com precisão absoluta os CONTRATOS GLOBAIS e os arquivos já gerados.
2. Gere o código 100% completo, funcional e sem omissões (NÃO use TODO ou reticências).
3. Coloque na primeira linha dentro do bloco de código o caminho relativo do arquivo no formato:
   // file: caminho/do/arquivo.ext   (ou # file: para python/yaml/bash)
4. Não responda com comandos de terminal. Responda apenas com o bloco de código markdown.`

			if systemPrompt != "" {
				sys = sys + "\n\n" + systemPrompt
			}

			var pb strings.Builder
			pb.WriteString(workerContext)
			pb.WriteString("\n[RECUPERAÇÃO CIRÚRGICA DE ARTEFATO AUSENTE]\n")
			pb.WriteString(fmt.Sprintf("Arquivo que você DEVE gerar: %s\n", d.FilePath))
			pb.WriteString(fmt.Sprintf("Diagnóstico do Gate 2: %s\n", d.Reason))
			pb.WriteString(fmt.Sprintf("Tarefa de Origem: %s (ID: %s)\n", task.Title, task.ID))
			pb.WriteString("Especificação original da tarefa:\n")
			pb.WriteString(task.Spec)
			pb.WriteString(fmt.Sprintf("\n\nGere o arquivo '%s' completo agora.", d.FilePath))

			taskCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			res, err := runner.Execute(taskCtx, client.RequestOptions{
				Model:        model,
				SystemPrompt: sys,
				Prompt:       pb.String(),
				Stream:       false,
			})
			cancel()

			if err != nil {
				if term != nil {
					term.LogWarn("Falha ao invocar runner para auto-recuperação de '%s': %v", d.FilePath, err)
				}
				continue
			}

			// Clean context sanitization and recording
			sanitized := SanitizeWorkerDelivery(res.Text, []string{d.FilePath})
			recoveredContent := ""

			if content, ok := sanitized.Files[d.FilePath]; ok && len(strings.TrimSpace(content)) > 0 {
				recoveredContent = content
			} else if content, ok := sanitized.Files[cleanFilePath(d.FilePath)]; ok && len(strings.TrimSpace(content)) > 0 {
				recoveredContent = content
			} else if len(sanitized.Files) == 1 {
				for _, c := range sanitized.Files {
					if len(strings.TrimSpace(c)) > 0 {
						recoveredContent = c
						break
					}
				}
			}

			if recoveredContent == "" {
				stripped := stripCodeFences(res.Text)
				if len(strings.TrimSpace(stripped)) > 0 {
					recoveredContent = stripped
				}
			}

			if len(recoveredContent) > 0 {
				bb.RecordFile(d.FilePath, task.Title, recoveredContent, "Gate2-SelfHeal")
				bb.RecordLesson("gate2-self-heal", d.Reason, fmt.Sprintf("Arquivo %s recuperado cirurgicamente após falha no Gate 2", d.FilePath), d.FilePath)

				if outDir != "" {
					_ = MaterializeFiles(bb, []string{d.FilePath}, outDir)
				}
				if term != nil {
					term.LogInfo("✅ [Gate 2 Self-Healing] Arquivo '%s' recuperado com sucesso (%d bytes)!",
						d.FilePath, len(recoveredContent))
				}
			} else if term != nil {
				term.LogError("❌ [Gate 2 Self-Healing] Não foi possível extrair código válido para '%s'", d.FilePath)
			}
		}

		// Re-evaluate defects after attempt
		defects = FindStageDefects(bb, stage)
		if len(defects) == 0 {
			break
		}
	}

	newEval := EvaluateStageGate(bb, stage)
	return newEval, nil
}


// EvaluateIntegrationGate audits the final project consistency and integrity (Gate 3).
func EvaluateIntegrationGate(bb *memory.Blackboard) *GateEvaluation {
	eval := &GateEvaluation{
		Type:        GateIntegrationQA,
		Passed:      true,
		Errors:      make([]string, 0),
		Warnings:    make([]string, 0),
		Artifacts:   make([]string, 0),
		EvaluatedAt: time.Now(),
	}

	files := bb.GetAllFiles()
	for _, f := range files {
		eval.Artifacts = append(eval.Artifacts, f.Path)
	}

	// Run supervisor cross-file audit
	rep := supervisor.ReviewProject(bb)
	if !rep.Passed {
		for _, errStr := range rep.Errors {
			eval.Errors = append(eval.Errors, errStr)
		}
	}

	// Verify no unresolved critical notes remain
	for _, note := range bb.GetNotes() {
		if note.Severity == "error" && !note.Fixed {
			eval.Errors = append(eval.Errors, fmt.Sprintf("[%s] %s", note.File, note.Description))
		}
	}

	if len(eval.Errors) > 0 {
		eval.Passed = false
		eval.Summary = fmt.Sprintf("Gate 3 Integração REPROVADO com %d inconsistência(s)", len(eval.Errors))
	} else {
		eval.Summary = fmt.Sprintf("Gate 3 Integração APROVADO: 0 inconsistências em %d arquivos", len(files))
	}

	return eval
}
