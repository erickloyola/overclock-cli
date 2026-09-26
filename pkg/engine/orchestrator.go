package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"overclock/pkg/client"
	"overclock/pkg/config"
	"overclock/pkg/inspector"
	"overclock/pkg/memory"
	"overclock/pkg/planner"
	"overclock/pkg/pruner"
	"overclock/pkg/supervisor"
	"overclock/pkg/ui"
	"overclock/pkg/verifier"
)

// Orchestrator coordinates execution across modes (pipe, map, direct).
type Orchestrator struct {
	cfg       *config.Config
	runner    Runner
	accounts  []string
	term      *ui.Terminal
	pruneOpts pruner.Options
}

// NewOrchestrator creates a new engine orchestrator.
func NewOrchestrator(cfg *config.Config, runner Runner, accounts []string, term *ui.Terminal) *Orchestrator {
	pruneOpts := pruner.DefaultOptions()
	pruneOpts.Level = cfg.PruneLevel
	if cfg.PruneLevel == "code" {
		pruneOpts.StripComments = true
	} else if cfg.PruneLevel == "log" {
		pruneOpts.SimplifyLogs = true
	} else if cfg.PruneLevel == "aggressive" {
		pruneOpts.StripComments = true
		pruneOpts.SimplifyLogs = true
	}

	return &Orchestrator{
		cfg:       cfg,
		runner:    runner,
		accounts:  accounts,
		term:      term,
		pruneOpts: pruneOpts,
	}
}

// RunCreateProject executes the intelligent multi-instance creation pipeline.
func (o *Orchestrator) RunCreateProject(
	ctx context.Context,
	prompt string,
	outDir string,
	contextFiles []string,
	lifecycle LifecycleOptions,
) error {
	t0 := time.Now()
	if outDir == "" {
		outDir = "./generated-project"
	}

	o.term.LogInfo("⚡ INICIANDO MODO CRIAÇÃO DE PROJETO NO OVERCLOCK")
	o.term.LogInfo("📁 Diretório de destino: %s", outDir)
	o.term.LogInfo("⚙️  Concorrência de workers: %d", o.cfg.Concurrency)

	// 1. Inspect host system
	env := inspector.InspectSystem(ctx)
	o.term.LogInfo("🔍 Ambiente detectado: %s/%s (%d CPUs)", env.OS, env.Arch, env.CPUCores)
	if len(env.Recommended) > 0 {
		var recs []string
		for k, v := range env.Recommended {
			recs = append(recs, fmt.Sprintf("%s=%s", k, v))
		}
		o.term.LogInfo("🛠️  Recomendações do sistema: %s", strings.Join(recs, ", "))
	}

	// 2. Load context files if any
	contextData, err := inspector.LoadContextFiles(contextFiles, o.pruneOpts)
	if err != nil {
		o.term.LogWarn("Aviso ao carregar contexto: %v", err)
	}

	// 3. Stage 1: Architect & Planner
	o.term.LogInfo("📐 [Instância 1: Arquiteto] Desenhando blueprint de software e contratos globais...")
	bb, err := planner.PlanProject(ctx, o.runner, prompt, env.SummaryString(), contextData, o.cfg.Model, o.cfg.Concurrency)
	if err != nil {
		return fmt.Errorf("falha no Arquiteto: %w", err)
	}

	manifest := bb.GetManifest()
	o.term.LogInfo("📋 Projeto planejado: '%s' | Stack: %s | Gerenciador: %s",
		manifest.Name, manifest.Stack, manifest.PackageManager)

	// Merge global cross-project knowledge
	if gm, gmErr := memory.LoadGlobalMemory(); gmErr == nil && gm != nil {
		bb.MergeGlobalMemory(gm)
		if len(gm.Facts) > 0 {
			o.term.LogInfo("🧠 %d fatos e convenções herdados da memória global do usuário", len(gm.Facts))
		}
	}

	// 4. Stage 2: Distribution Verifier & Auditor
	o.term.LogInfo("🔍 [Instância 2: Verificador] Auditando distribuição de tarefas e grafo de dependências...")
	auditRep, err := verifier.AuditBlueprint(bb)
	if err != nil {
		return fmt.Errorf("falha na auditoria do plano: %w", err)
	}

	if len(auditRep.AutoPatchesApplied) > 0 {
		for _, p := range auditRep.AutoPatchesApplied {
			o.term.LogInfo("   🔧 Ajuste automático aplicado: %s", p)
		}
	}
	o.term.LogInfo("✅ Plano validado: %d tarefas | %d arquivos | Paralelismo máximo do DAG: %d workers",
		auditRep.TotalTasks, auditRep.TotalFiles, auditRep.MaxConcurrency)

	// Validation Gate 1: Blueprint & Contracts Specification Gate
	o.term.LogInfo("🚪 [Validation Gate 1] Avaliando especificações de contratos e consistência do blueprint...")
	gate1 := EvaluateContractSpecGate(bb)
	_ = memory.AppendEvent(outDir, memory.EventGateEvaluated, gate1)
	if !gate1.Passed {
		return fmt.Errorf("Gate 1 (Especificação/Contrato) reprovado: %s", strings.Join(gate1.Errors, "; "))
	}
	o.term.LogInfo("✅ [GATE 1 GREEN] %s", gate1.Summary)

	// Persist initial blueprint immediately so no planning work is lost
	_ = bb.SaveState(outDir)

	// 5. Stage 3: DAG Concurrent Execution with Shared Memory & Reactive Event-Driven Handoff
	o.term.LogInfo("⚡ [Workers de Execução] Disparando workers concorrentes com handoff orientado a eventos...")
	dagOpts := DAGOptions{
		Concurrency:  o.cfg.Concurrency,
		Accounts:     o.accounts,
		Model:        o.cfg.Model,
		SystemPrompt: o.cfg.SystemPrompt,
		UseWorktrees: o.cfg.UseWorktrees,
	}
	if err := RunDAGWithOptions(ctx, o.runner, bb, outDir, dagOpts, o.term); err != nil {
		o.term.LogInfo("💡 Dica: Para continuar de onde parou após ajustar o problema, execute: overclock resume %s", outDir)
		return fmt.Errorf("falha durante execução do DAG: %w", err)
	}

	// 6. Stage 4: Cross-File Consistency Reviewer (Supervisor)
	o.term.LogInfo("🕵️ [Instância 4: Supervisor] Analisando consistência cruzada entre todos os arquivos gerados...")
	supRep := supervisor.ReviewProject(bb)
	if supRep.Passed {
		o.term.LogInfo("✅ Consistência aprovada: 0 erros críticos em %d arquivos checados!", supRep.TotalFilesChecked)
	} else {
		o.term.LogWarn("⚠️ O Supervisor detectou %d inconsistências. Aplicando correções...", len(supRep.Errors))
		for _, errDesc := range supRep.Errors {
			o.term.LogWarn("   • %s", errDesc)
		}

		fileErrors := make(map[string][]string)
		for _, note := range bb.GetNotes() {
			if note.Severity == "error" && !note.Fixed {
				fileErrors[note.File] = append(fileErrors[note.File], note.Description)
			}
		}

		for file, descs := range fileErrors {
			combinedDesc := strings.Join(descs, "; ")
			o.term.LogInfo("🔧 Supervisor aplicando patch em %s (%s)...", file, combinedDesc)
			if patchErr := supervisor.AutoPatchFile(ctx, o.runner, bb, file, combinedDesc, o.cfg.Model); patchErr != nil {
				o.term.LogError("❌ Falha ao aplicar patch em %s: %v", file, patchErr)
			} else {
				bb.MarkNotesFixed(file)
				o.term.LogInfo("✅ Patch aplicado com sucesso em %s", file)
			}
		}
	}

	// Validation Gate 3: Integration & QA Gate
	o.term.LogInfo("🚪 [Validation Gate 3] Validando auditoria de integração e consistência global...")
	gate3 := EvaluateIntegrationGate(bb)
	_ = memory.AppendEvent(outDir, memory.EventGateEvaluated, gate3)
	if !gate3.Passed {
		o.term.LogWarn("⚠️ [GATE 3 WARN] Inconsistências remanescentes no projeto: %s", strings.Join(gate3.Errors, "; "))
	} else {
		o.term.LogInfo("✅ [GATE 3 GREEN] %s", gate3.Summary)
	}

	// 7. Materialize Files to Disk
	o.term.LogInfo("📦 [Materializador] Gravando arquivos no disco em '%s'...", outDir)
	filesWritten, err := MaterializeBlackboard(bb, outDir)
	if err != nil {
		return fmt.Errorf("falha ao gravar arquivos do projeto: %w", err)
	}

	// 8. Execute Lifecycle Hooks (--install, --verify, --git)
	if err := ExecuteLifecycleHooks(ctx, bb, outDir, lifecycle, o.runner, o.cfg.Model, o.term); err != nil {
		o.term.LogError("Erro nos lifecycle hooks: %v", err)
	}

	totalDur := time.Since(t0)

	// 9. Pretty Final Report
	fmt.Printf("\n" + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset + "\n")
	fmt.Printf(" " + ui.Bold + ui.Green + "🎉 PROJETO CRIADO COM SUCESSO PELO OVERCLOCK!" + ui.Reset + "\n")
	fmt.Printf(" " + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset + "\n\n")
	fmt.Printf("   • "+ui.Bold+"Nome:"+ui.Reset+"           %s\n", manifest.Name)
	fmt.Printf("   • "+ui.Bold+"Stack:"+ui.Reset+"          %s\n", manifest.Stack)
	fmt.Printf("   • "+ui.Bold+"Destino:"+ui.Reset+"        %s\n", outDir)
	fmt.Printf("   • "+ui.Bold+"Arquivos:"+ui.Reset+"       %d arquivos gerados\n", len(filesWritten))
	fmt.Printf("   • "+ui.Bold+"Tempo Total:"+ui.Reset+"    %.2fs (aceleração com %d workers)\n", totalDur.Seconds(), o.cfg.Concurrency)
	fmt.Printf("   • "+ui.Bold+"Estado Salvo:"+ui.Reset+"   %s/.overclock/state.json\n\n", outDir)

	fmt.Println(" " + ui.Bold + "📁 Arquivos Gerados:" + ui.Reset)
	for _, f := range filesWritten {
		fmt.Printf("   ├── %s (%d bytes)\n", f.Path, f.Bytes)
	}

	runCmd := manifest.RunCommand
	if runCmd == "" {
		pm := strings.ToLower(manifest.PackageManager)
		switch pm {
		case "bun":
			runCmd = "bun dev"
		case "go":
			runCmd = "go run ."
		case "cargo":
			runCmd = "cargo run"
		case "pip", "python", "poetry":
			runCmd = "python3 main.py"
		case "make":
			runCmd = "make run"
		case "npm", "pnpm", "yarn":
			runCmd = "npm run dev"
		default:
			runCmd = "./run.sh"
		}
	}

	fmt.Printf("\n " + ui.Bold + "🚀 Para inicializar e rodar o projeto:" + ui.Reset + "\n")
	fmt.Printf("   "+ui.Green+"cd %s"+ui.Reset, outDir)
	if !lifecycle.InstallDeps && manifest.PackageManager != "" {
		switch strings.ToLower(manifest.PackageManager) {
		case "go":
			fmt.Printf(" && " + ui.Green + "go mod tidy" + ui.Reset)
		case "bun":
			fmt.Printf(" && " + ui.Green + "bun install" + ui.Reset)
		case "cargo":
			fmt.Printf(" && " + ui.Green + "cargo check" + ui.Reset)
		case "pip":
			fmt.Printf(" && " + ui.Green + "pip install -r requirements.txt" + ui.Reset)
		case "poetry":
			fmt.Printf(" && " + ui.Green + "poetry install" + ui.Reset)
		case "make":
			fmt.Printf(" && " + ui.Green + "make" + ui.Reset)
		case "none", "":
			// No install step needed
		default:
			fmt.Printf(" && "+ui.Green+"%s install"+ui.Reset, manifest.PackageManager)
		}
	}
	fmt.Printf(" && "+ui.Green+"%s"+ui.Reset+"\n\n", runCmd)

	return nil
}

// RunPipe executes pipe mode: either single-stream or multi-instance decomposed prompt.
func (o *Orchestrator) RunPipe(ctx context.Context, prompt string, stdinData string) error {
	// If out-dir is specified, route to intelligent project creation!
	if o.cfg.OutDir != "" {
		return o.RunCreateProject(ctx, prompt, o.cfg.OutDir, nil, LifecycleOptions{})
	}

	// If prompt is empty but stdin has data, treat stdin as the primary prompt
	if prompt == "" && stdinData != "" {
		prompt = stdinData
		stdinData = ""
	}

	var pruneStats pruner.Stats
	contextData := stdinData

	if o.cfg.Prune && contextData != "" {
		contextData, pruneStats = pruner.Prune(contextData, o.pruneOpts)
		o.term.LogInfo("Context pruned: %d -> %d bytes (saved %.1f%%)",
			pruneStats.OriginalBytes, pruneStats.PrunedBytes, pruneStats.SavingsPct)
	}

	// If concurrency > 1 or decompose requested, use parallel multi-instance acceleration
	if o.cfg.Concurrency > 1 && (len(prompt)+len(contextData) > 150) {
		res, err := ExecuteDecomposed(
			ctx,
			o.runner,
			prompt,
			contextData,
			o.cfg.SystemPrompt,
			o.cfg.Model,
			o.cfg.Concurrency,
			o.accounts,
			o.term,
		)
		if err != nil {
			return fmt.Errorf("erro no overclock paralelo: %w", err)
		}

		if o.cfg.JSONOutput {
			o.term.EmitResult("stdin", res, &pruneStats)
		} else {
			fmt.Println(res.Text)
		}
		o.term.PrintStats(res, &pruneStats)
		return nil
	}

	// Single worker streaming execution
	streamActive := o.cfg.Stream
	if o.cfg.JSONOutput {
		streamActive = false
	}

	o.term.StartThinking(o.cfg.Model)
	firstToken := true

	reqOpts := client.RequestOptions{
		Model:        o.cfg.Model,
		SystemPrompt: o.cfg.SystemPrompt,
		Prompt:       prompt,
		ContextData:  contextData,
		Temperature:  o.cfg.Temperature,
		Stream:       streamActive,
		OnToken: func(chunk string) error {
			if firstToken {
				firstToken = false
				o.term.StopThinking()
			}
			o.term.PrintChunk(chunk)
			return nil
		},
	}

	res, err := o.runner.Execute(ctx, reqOpts)
	o.term.StopThinking()
	if err != nil {
		return fmt.Errorf("execution error: %w", err)
	}

	if streamActive && o.term.IsTTY() {
		fmt.Println()
	}

	if !streamActive || o.cfg.JSONOutput {
		o.term.EmitResult("stdin", res, &pruneStats)
	}

	o.term.PrintStats(res, &pruneStats)
	return nil
}

// RunMapFiles processes a list of files concurrently using the worker pool.
func (o *Orchestrator) RunMapFiles(ctx context.Context, prompt string, files []string) error {
	if len(files) == 0 {
		return fmt.Errorf("nenhum arquivo fornecido para o modo map")
	}

	tasks := make(chan Task, o.cfg.Concurrency*2)
	results := make(chan TaskResult, o.cfg.Concurrency*2)

	// Create and launch worker pool
	pool := NewPool(o.cfg.Concurrency, o.cfg.Model, o.runner, o.cfg.Prune, o.pruneOpts)
	go pool.Run(ctx, tasks, results)

	// Producer goroutine: read files and feed tasks
	go func() {
		defer close(tasks)
		for i, filePath := range files {
			select {
			case <-ctx.Done():
				return
			default:
			}

			data, err := os.ReadFile(filePath)
			if err != nil {
				o.term.LogError("Erro ao ler arquivo %s: %v", filePath, err)
				continue
			}

			tasks <- Task{
				Index:        i,
				Source:       filePath,
				Content:      string(data),
				Prompt:       prompt,
				SystemPrompt: o.cfg.SystemPrompt,
			}
		}
	}()

	// Consumer: process results as they arrive
	successCount := 0
	errorCount := 0
	var totalDuration time.Duration
	start := time.Now()

	for tr := range results {
		if tr.Err != nil {
			errorCount++
			o.term.LogError("[%s] Falha na execução: %v", tr.Task.Source, tr.Err)
			continue
		}

		successCount++
		totalDuration += tr.Result.TotalDuration
		o.term.EmitResult(tr.Task.Source, tr.Result, &tr.PruneStats)
	}

	overallElapsed := time.Since(start)
	o.term.LogInfo("Map concluído: %d sucessos, %d falhas em %v (concorrência: %d)",
		successCount, errorCount, overallElapsed.Round(time.Millisecond), o.cfg.Concurrency)

	return nil
}

// RunMapLines reads lines from STDIN and executes prompts concurrently per line.
func (o *Orchestrator) RunMapLines(ctx context.Context, prompt string, r io.Reader) error {
	tasks := make(chan Task, o.cfg.Concurrency*2)
	results := make(chan TaskResult, o.cfg.Concurrency*2)

	pool := NewPool(o.cfg.Concurrency, o.cfg.Model, o.runner, o.cfg.Prune, o.pruneOpts)
	go pool.Run(ctx, tasks, results)

	go func() {
		defer close(tasks)
		scanner := bufio.NewScanner(r)
		idx := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case tasks <- Task{
				Index:        idx,
				Source:       fmt.Sprintf("line:%d", idx+1),
				Content:      line,
				Prompt:       prompt,
				SystemPrompt: o.cfg.SystemPrompt,
			}:
			}
			idx++
		}
	}()

	for tr := range results {
		if tr.Err != nil {
			o.term.LogError("[%s] Falha: %v", tr.Task.Source, tr.Err)
			continue
		}
		o.term.EmitResult(tr.Task.Source, tr.Result, &tr.PruneStats)
	}

	return nil
}

// ReadAllStdin reads all contents from STDIN if available.
func ReadAllStdin() (string, bool, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return "", false, err
	}

	// Check if data is being piped in
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		var sb strings.Builder
		reader := bufio.NewReader(os.Stdin)
		buf := make([]byte, 32*1024)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", false, err
			}
		}
		return sb.String(), true, nil
	}

	return "", false, nil
}

// RunResumeProject resumes project creation from a saved .overclock/state.json.
func (o *Orchestrator) RunResumeProject(
	ctx context.Context,
	projectDir string,
	lifecycle LifecycleOptions,
) error {
	t0 := time.Now()
	if projectDir == "" {
		projectDir = "."
	}

	stateFile := filepath.Join(projectDir, ".overclock", "state.json")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		return fmt.Errorf("nenhum estado anterior do Overclock encontrado em '%s'. Certifique-se de indicar o caminho da pasta do projeto", stateFile)
	}

	o.term.LogInfo("⚡ INICIANDO RETOMADA DE PROJETO NO OVERCLOCK")
	o.term.LogInfo("📁 Diretório do projeto: %s", projectDir)
	o.term.LogInfo("📄 Lendo estado de: %s", stateFile)

	bb, err := memory.LoadState(stateFile)
	if err != nil {
		return fmt.Errorf("falha ao carregar estado do projeto: %w", err)
	}

	manifest := bb.GetManifest()
	o.term.LogInfo("📋 Projeto: '%s' | Stack: %s | Gerenciador: %s",
		manifest.Name, manifest.Stack, manifest.PackageManager)

	// Sync any existing files from disk into bb.Files if edited
	for _, f := range bb.GetAllFiles() {
		clean := cleanFilePath(f.Path)
		diskPath := filepath.Join(projectDir, clean)
		if diskBytes, err := os.ReadFile(diskPath); err == nil {
			if string(diskBytes) != f.Content {
				f.Content = string(diskBytes)
				f.Bytes = len(diskBytes)
			}
		}
	}

	// Analyze tasks and reset non-completed to Pending
	allTasks := bb.GetAllTasks()
	var completedCount int
	var pendingCount int
	var failedCount int

	for _, t := range allTasks {
		switch t.Status {
		case memory.StatusCompleted:
			completedCount++
		case memory.StatusFailed:
			failedCount++
			t.Status = memory.StatusPending
			t.Error = ""
		case memory.StatusRunning:
			t.Status = memory.StatusPending
			t.Error = ""
		default:
			pendingCount++
		}
	}

	totalToRun := pendingCount + failedCount
	o.term.LogInfo("📊 Status das tarefas: %d concluídas | %d para executar (%d pendentes, %d recuperadas de falha)",
		completedCount, totalToRun, pendingCount, failedCount)

	if totalToRun > 0 {
		o.term.LogInfo("⚡ [Workers de Execução] Disparando workers concorrentes para finalizar tarefas restantes...")
		dagOpts := DAGOptions{
			Concurrency:  o.cfg.Concurrency,
			Accounts:     o.accounts,
			Model:        o.cfg.Model,
			SystemPrompt: o.cfg.SystemPrompt,
			UseWorktrees: o.cfg.UseWorktrees,
		}
		if err := RunDAGWithOptions(ctx, o.runner, bb, projectDir, dagOpts, o.term); err != nil {
			o.term.LogInfo("💡 Dica: Para tentar novamente de onde parou, execute: overclock resume %s", projectDir)
			return fmt.Errorf("falha durante retomada do DAG: %w", err)
		}
	} else {
		o.term.LogInfo("🎉 Todas as %d tarefas já foram concluídas anteriormente!", len(allTasks))
	}

	// Cross-File Consistency Reviewer (Supervisor)
	o.term.LogInfo("🕵️ [Instância 4: Supervisor] Analisando consistência cruzada entre todos os arquivos...")
	supRep := supervisor.ReviewProject(bb)
	if supRep.Passed {
		o.term.LogInfo("✅ Consistência aprovada: 0 erros críticos em %d arquivos checados!", supRep.TotalFilesChecked)
	} else {
		o.term.LogWarn("⚠️ O Supervisor detectou %d inconsistências. Aplicando correções...", len(supRep.Errors))
		for _, errDesc := range supRep.Errors {
			o.term.LogWarn("   • %s", errDesc)
		}

		fileErrors := make(map[string][]string)
		for _, note := range bb.GetNotes() {
			if note.Severity == "error" && !note.Fixed {
				fileErrors[note.File] = append(fileErrors[note.File], note.Description)
			}
		}

		for file, descs := range fileErrors {
			combinedDesc := strings.Join(descs, "; ")
			o.term.LogInfo("🔧 Supervisor aplicando patch em %s (%s)...", file, combinedDesc)
			if patchErr := supervisor.AutoPatchFile(ctx, o.runner, bb, file, combinedDesc, o.cfg.Model); patchErr != nil {
				o.term.LogError("❌ Falha ao aplicar patch em %s: %v", file, patchErr)
			} else {
				bb.MarkNotesFixed(file)
				o.term.LogInfo("✅ Patch aplicado com sucesso em %s", file)
			}
		}
	}

	// Materialize / sync final files
	o.term.LogInfo("📦 [Materializador] Sincronizando arquivos finais no disco em '%s'...", projectDir)
	filesWritten, err := MaterializeBlackboard(bb, projectDir)
	if err != nil {
		return fmt.Errorf("falha ao sincronizar arquivos do projeto: %w", err)
	}

	// Execute Lifecycle Hooks
	if err := ExecuteLifecycleHooks(ctx, bb, projectDir, lifecycle, o.runner, o.cfg.Model, o.term); err != nil {
		o.term.LogError("Erro nos lifecycle hooks: %v", err)
	}

	totalDur := time.Since(t0)

	// Pretty Final Report
	fmt.Printf("\n" + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset + "\n")
	fmt.Printf(" " + ui.Bold + ui.Green + "🎉 PROJETO RETOMADO E CONCLUÍDO COM SUCESSO!" + ui.Reset + "\n")
	fmt.Printf(" " + ui.Cyan + "═══════════════════════════════════════════════════════════════════════════════" + ui.Reset + "\n\n")
	fmt.Printf("   • "+ui.Bold+"Nome:"+ui.Reset+"           %s\n", manifest.Name)
	fmt.Printf("   • "+ui.Bold+"Stack:"+ui.Reset+"          %s\n", manifest.Stack)
	fmt.Printf("   • "+ui.Bold+"Destino:"+ui.Reset+"        %s\n", projectDir)
	fmt.Printf("   • "+ui.Bold+"Arquivos:"+ui.Reset+"       %d arquivos no projeto\n", len(filesWritten))
	fmt.Printf("   • "+ui.Bold+"Tempo Retomada:"+ui.Reset+" %.2fs\n", totalDur.Seconds())
	fmt.Printf("   • "+ui.Bold+"Estado Atual:"+ui.Reset+"   %s/.overclock/state.json\n\n", projectDir)

	fmt.Println(" " + ui.Bold + "📁 Arquivos do Projeto:" + ui.Reset)
	for _, f := range filesWritten {
		fmt.Printf("   ├── %s (%d bytes)\n", f.Path, f.Bytes)
	}

	runCmd := manifest.RunCommand
	if runCmd == "" {
		pm := strings.ToLower(manifest.PackageManager)
		switch pm {
		case "go":
			runCmd = "go run ."
		case "cargo":
			runCmd = "cargo run"
		case "pip", "python", "poetry", "uv":
			runCmd = "python3 main.py"
		case "make":
			runCmd = "make run"
		case "bun":
			runCmd = "bun dev"
		case "npm", "pnpm", "yarn":
			runCmd = "npm run dev"
		default:
			runCmd = "./run.sh"
		}
	}
	fmt.Printf("\n " + ui.Bold + "🚀 Para rodar o projeto agora:" + ui.Reset + "\n")
	fmt.Printf("   cd %s && %s\n\n", projectDir, runCmd)

	return nil
}
