package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"overclock/pkg/auth"
	"overclock/pkg/client"
	"overclock/pkg/config"
	"overclock/pkg/engine"
	"overclock/pkg/pool"
	"overclock/pkg/ui"
)

const version = "2.0.0"

func printUsage() {
	banner := `
  ██████╗ ██╗   ██╗███████╗██████╗  ██████╗██╗      ██████╗  ██████╗██╗  ██╗
 ██╔═══██╗██║   ██║██╔════╝██╔══██╗██╔════╝██║     ██╔═══██╗██╔════╝██║ ██╔╝
 ██║   ██║██║   ██║█████╗  ██████╔╝██║     ██║     ██║   ██║██║     █████╔╝ 
 ██║   ██║╚██╗ ██╔╝██╔══╝  ██╔══██╗██║     ██║     ██║   ██║██║     ██╔═██╗ 
 ╚██████╔╝ ╚████╔╝ ███████╗██║  ██║╚██████╗███████╗╚██████╔╝╚██████╗██║  ██╗
  ╚═════╝   ╚═══╝  ╚══════╝╚═╝  ╚═╝ ╚═════╝╚══════╝ ╚═════╝  ╚═════╝╚═╝  ╚═╝
 High-Performance AI Project Orchestration & Acceleration Engine for Linux
 Version: ` + version + ` (Multi-Instance DAG Swarm & Shared Memory Blackboard)
`
	fmt.Print(banner)
	fmt.Print(`
USO:
  overclock create [FLAGS] "PROMPT"              Criação autônoma de projeto com DAG e Memória
  overclock resume [FLAGS] [PASTA]               Retoma um projeto interrompido a partir do estado salvo
  overclock memory [COMANDO] [PASTA]             Inspeciona fatos, lições, histórico e rollback da memória
  overclock mcp [--dir PASTA]                    Inicia o servidor MCP nativo (Model Context Protocol)
  overclock [FLAGS] "PROMPT"                     Modo Pipe/Direto (lê STDIN se disponível)
  overclock map [FLAGS] "PROMPT" [ARQUIVOS...]   Processa múltiplos arquivos em paralelo
  overclock lines [FLAGS] "PROMPT"               Processa STDIN linha a linha em paralelo

CRIAÇÃO DE PROJETOS MULTI-INSTÂNCIA:
  # Criação acelerada com 8 workers paralelos, auto-instalação e verificação de build:
  overclock create -j 8 --out-dir ./meu-app -i --verify "Crie uma API REST em Go com Gin e JWT"

  # Retomar um projeto interrompido exatamente de onde parou:
  overclock resume ./meu-app -j 8 -i

  # Inspecionar fatos e lições aprendidas na memória de um projeto:
  overclock memory facts ./meu-app
  overclock memory history src/main.go ./meu-app

  # Dashboard completo com inspeção de contexto de um schema local:
  overclock create -j 8 --out-dir ./dashboard --context ./schema.sql "Crie um dashboard em React + Vite"

GERENCIAMENTO DE MÚLTIPLAS CONTAS:
  overclock accounts                             Lista todas as contas Google cadastradas
  overclock switch <PERFIL>                      Alterna a conta ativa no agy/overclock
  overclock login [--name PERFIL]                Conecta uma nova conta Google via OAuth
  overclock logout [PERFIL]                      Remove o perfil de uma conta

UTILITÁRIOS:
  overclock apply resultado.md --out-dir ./pasta Materializa markdown salvo em arquivos reais
  overclock memory -h                            Ajuda do sistema de memória compartilhada

FLAGS:
  -j, --jobs int           Número de workers concorrentes (padrão: 4, recomendado: 8)
  -o, --out-dir string     Diretório de saída para os arquivos do projeto
  -i, --install            Instala dependências automaticamente após a geração
      --verify             Testa o build com o compilador real e aciona auto-correção se falhar
      --git                Inicializa repositório Git no projeto gerado
      --worktrees          Isola workers concorrentes em Git Worktrees dedicadas
      --context string     Arquivo ou schema local para injetar no planejamento
      --from string        Diretório de referência local para contexto
  -m, --model string       Modelo (padrão agy: gemini-3.8-flash-high | api: gemini-3.6-flash)
      --engine string      Engine de IA: 'agy' (Antigravity nativo) ou 'api' (REST)
      --account string     Seleciona um perfil de conta específico
  -s, --system string      Instrução de sistema adicional
      --stream             Habilita streaming SSE em tempo real (modo pipe)
      --no-stream          Desabilita streaming em tempo real
      --json               Saída em formato JSON estruturado
      --prune              Habilita compressão e poda de contexto
      --prune-level string Nível de poda: basic, code, log, aggressive
  -t, --timeout duration   Timeout por requisição (padrão: 90s)
  -v, --verbose            Exibe diagnósticos e telemetria detalhada no stderr
  -h, --help               Exibe esta ajuda
`)
}

func handleLogin(args []string) {
	_, _ = config.LoadAPIKeys("")

	name := "default"
	clientID := ""
	clientSecret := ""
	scope := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-n" || arg == "--name") && i+1 < len(args) {
			name = args[i+1]
			i++
		} else if (arg == "--client-id" || arg == "-c") && i+1 < len(args) {
			clientID = args[i+1]
			i++
		} else if (arg == "--client-secret" || arg == "-s") && i+1 < len(args) {
			clientSecret = args[i+1]
			i++
		} else if (arg == "--scope") && i+1 < len(args) {
			scope = args[i+1]
			i++
		} else if !strings.HasPrefix(arg, "-") && i == 0 {
			name = arg
		}
	}

	fmt.Printf("⚡ Iniciando autenticação OAuth 2.0 para o perfil '%s'...\n", name)
	acc, err := auth.StartLoginFlow(name, clientID, clientSecret, scope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Falha no login: %v\n\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Conta conectada com sucesso!\n")
	fmt.Printf("   • Perfil: %s\n", acc.Name)
	if acc.Email != "" {
		fmt.Printf("   • Email:  %s\n", acc.Email)
	}
	fmt.Printf("   • Tokens armazenados em ~/.config/overclock/accounts/%s.json\n\n", acc.Name)

	_, _ = auth.SwitchAccount(name)
	fmt.Println("Conta ativada no Antigravity CLI e no Overclock!")
}

func handleAccounts() {
	accounts, err := auth.ListAccounts()
	currentEmail, _ := auth.GetCurrentAgyEmail()

	if err != nil || len(accounts) == 0 {
		fmt.Println("\nNenhuma conta cadastrada em ~/.config/overclock/accounts/.")
		if currentEmail != "" {
			fmt.Printf("Conta ativa no agy: %s\n\n", currentEmail)
		}
		fmt.Println("Para cadastrar nova conta: overclock login --name <perfil>")
		return
	}

	fmt.Printf("\n📋 Contas Google / Antigravity cadastradas (%d):\n", len(accounts))
	for _, a := range accounts {
		activeMarker := "⚪ [SALVA] "
		if a.Email != "" && a.Email == currentEmail {
			activeMarker = "🟢 [ATIVA] "
		}
		fmt.Printf(" %s[%s] %s (expira: %s)\n", activeMarker, a.Name, a.Email, a.Expiry.Format("02/01 15:04"))
	}
	fmt.Println("\nPara alternar a conta padrão: overclock switch <NOME>")
	fmt.Println()
}

func handleSwitch(args []string) {
	if len(args) == 0 {
		fmt.Println("Uso: overclock switch <NOME_DA_CONTA>")
		os.Exit(1)
	}
	name := args[0]
	acc, err := auth.SwitchAccount(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Erro ao alternar conta: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Conta ativa alternada com sucesso para '%s' (%s)!\n", acc.Name, acc.Email)
	fmt.Println("O comando 'agy' e as novas instâncias do Overclock utilizarão esta conta.")
}

func handleLogout(args []string) {
	name := "default"
	if len(args) > 0 {
		name = args[0]
	}
	if err := auth.RemoveAccount(name); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao remover conta '%s': %v\n", name, err)
		os.Exit(1)
	}
	fmt.Printf("✅ Conta '%s' removida com sucesso.\n", name)
}

func handleApply(args []string) {
	if len(args) == 0 {
		fmt.Println("Uso: overclock apply <ARQUIVO_MARKDOWN> [--out-dir <PASTA>]")
		os.Exit(1)
	}
	srcFile := args[0]
	outDir := "."

	for i := 1; i < len(args); i++ {
		if (args[i] == "--out-dir" || args[i] == "-o" || args[i] == "--dir") && i+1 < len(args) {
			outDir = args[i+1]
			i++
		}
	}

	data, err := os.ReadFile(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Erro ao ler arquivo %s: %v\n", srcFile, err)
		os.Exit(1)
	}

	fmt.Printf("⚡ Materializando projeto a partir de '%s' em '%s'...\n", srcFile, outDir)
	files, err := engine.MaterializeProject(string(data), outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Erro ao criar arquivos: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n📦 PROJETO GERADO COM SUCESSO EM: %s (%d arquivos criados)\n", outDir, len(files))
	for _, f := range files {
		fmt.Printf("   📁 %s (%d bytes)\n", f.Path, f.Bytes)
	}
	fmt.Printf("\n💡 Para inicializar o projeto:\n   cd %s\n\n", outDir)
}

func main() {
	cfg := config.DefaultConfig()

	var (
		flagJobs       int
		flagModel      string
		flagEngine     string
		flagAccount    string
		flagOutDir     string
		flagInstall    bool
		flagVerify     bool
		flagGit        bool
		flagWorktrees  bool
		flagContext    string
		flagFrom       string
		flagSystem     string
		flagStream     bool
		flagNoStream   bool
		flagJSON       bool
		flagPrune      bool
		flagPruneLevel string
		flagKeysFile   string
		flagTimeout    time.Duration
		flagVerbose    bool
		flagHelp       bool
		flagVersion    bool
	)

	// Check subcommands before flag parsing
	args := os.Args[1:]
	mode := "pipe"

	if len(args) > 0 {
		switch args[0] {
		case "login":
			handleLogin(args[1:])
			return
		case "accounts":
			handleAccounts()
			return
		case "switch":
			handleSwitch(args[1:])
			return
		case "logout":
			handleLogout(args[1:])
			return
		case "apply":
			handleApply(args[1:])
			return
		case "memory":
			handleMemory(args[1:])
			return
		case "mcp":
			handleMCP(args[1:])
			return
		case "create":
			mode = "create"
			args = args[1:]
		case "resume":
			mode = "resume"
			args = args[1:]
		case "map":
			mode = "map"
			args = args[1:]
		case "lines":
			mode = "lines"
			args = args[1:]
		}
	}

	fs := flag.NewFlagSet("overclock", flag.ExitOnError)
	fs.Usage = printUsage

	fs.IntVar(&flagJobs, "j", 4, "Número de workers concorrentes")
	fs.IntVar(&flagJobs, "jobs", 4, "Número de workers concorrentes")
	fs.StringVar(&flagModel, "m", "", "Modelo")
	fs.StringVar(&flagModel, "model", "", "Modelo")
	fs.StringVar(&flagEngine, "engine", "", "Engine de IA: 'agy' ou 'api'")
	fs.StringVar(&flagAccount, "account", "", "Perfil de conta a utilizar")
	fs.StringVar(&flagOutDir, "out-dir", "", "Diretório de saída para materializar os arquivos do projeto")
	fs.StringVar(&flagOutDir, "o", "", "Diretório de saída para materializar os arquivos do projeto")
	fs.StringVar(&flagOutDir, "create", "", "Diretório de saída para materializar os arquivos do projeto")
	fs.BoolVar(&flagInstall, "install", false, "Instala automaticamente dependências após geração")
	fs.BoolVar(&flagInstall, "i", false, "Instala automaticamente dependências após geração")
	fs.BoolVar(&flagVerify, "verify", false, "Verifica o build com o compilador real e auto-corrige")
	fs.BoolVar(&flagGit, "git", false, "Inicializa repositório Git")
	fs.BoolVar(&flagWorktrees, "worktrees", false, "Isola workers concorrentes em Git Worktrees paralelas")
	fs.StringVar(&flagContext, "context", "", "Arquivo ou schema local para contexto")
	fs.StringVar(&flagFrom, "from", "", "Diretório de referência local para contexto")
	fs.StringVar(&flagSystem, "s", "", "System instruction")
	fs.StringVar(&flagSystem, "system", "", "System instruction")
	fs.BoolVar(&flagStream, "stream", false, "Streaming SSE")
	fs.BoolVar(&flagNoStream, "no-stream", false, "Desabilitar streaming")
	fs.BoolVar(&flagJSON, "json", false, "Saída JSON estruturada")
	fs.BoolVar(&flagPrune, "prune", false, "Ativar poda de contexto")
	fs.StringVar(&flagPruneLevel, "prune-level", "basic", "Nível de poda (basic, code, log, aggressive)")
	fs.StringVar(&flagKeysFile, "keys-file", "", "Arquivo de chaves de API")
	fs.DurationVar(&flagTimeout, "t", 90*time.Second, "Timeout da requisição")
	fs.DurationVar(&flagTimeout, "timeout", 90*time.Second, "Timeout da requisição")
	fs.BoolVar(&flagVerbose, "v", false, "Verbose telemetry")
	fs.BoolVar(&flagVerbose, "verbose", false, "Verbose telemetry")
	fs.BoolVar(&flagHelp, "h", false, "Exibe ajuda")
	fs.BoolVar(&flagHelp, "help", false, "Exibe ajuda")
	fs.BoolVar(&flagVersion, "version", false, "Exibe versão")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao processar flags: %v\n", err)
		os.Exit(1)
	}

	if flagHelp {
		printUsage()
		os.Exit(0)
	}

	if flagVersion {
		fmt.Printf("overclock version %s (go runtime %s)\n", version, runtime.Version())
		os.Exit(0)
	}

	cfg.Concurrency = flagJobs
	cfg.SystemPrompt = flagSystem
	cfg.JSONOutput = flagJSON
	cfg.Prune = flagPrune
	cfg.PruneLevel = flagPruneLevel
	cfg.Timeout = flagTimeout
	cfg.UseWorktrees = flagWorktrees
	cfg.Verbose = flagVerbose
	cfg.OutDir = flagOutDir

	term := ui.NewTerminal(cfg.JSONOutput, cfg.Verbose)
	if flagNoStream || cfg.JSONOutput {
		cfg.Stream = false
	} else if flagStream || (mode == "pipe" && term.IsTTY()) {
		cfg.Stream = true
	}

	// 1. Detect and choose Engine
	agyAvailable := false
	if _, err := exec.LookPath("agy"); err == nil {
		agyAvailable = true
	} else if home, _ := os.UserHomeDir(); home != "" {
		if _, err := os.Stat(filepath.Join(home, ".local", "bin", "agy")); err == nil {
			agyAvailable = true
		}
	}

	var runner engine.Runner
	var configuredAccounts []string

	savedAccs, _ := auth.ListAccounts()
	for _, a := range savedAccs {
		configuredAccounts = append(configuredAccounts, a.Name)
	}
	if len(configuredAccounts) == 0 {
		configuredAccounts = []string{"default"}
	}

	if flagEngine == "agy" || (flagEngine == "" && agyAvailable) {
		selectedModel := flagModel
		if selectedModel == "" {
			selectedModel = "gemini-3.8-flash-high"
		}
		cfg.Model = selectedModel

		agyRunner, err := engine.NewAgyRunner("", selectedModel, flagAccount)
		if err != nil {
			term.LogError("Falha ao inicializar AgyRunner: %v", err)
			os.Exit(1)
		}
		runner = agyRunner

		activeEmail, _ := auth.GetCurrentAgyEmail()
		term.LogInfo("⚡ Engine ATIVA: Antigravity CLI (agy) | Modelo: %s | Conta: %s | Workers: %d",
			cfg.Model, activeEmail, cfg.Concurrency)
	} else {
		// API Engine fallback
		selectedModel := flagModel
		if selectedModel == "" {
			selectedModel = "gemini-3.6-flash"
		}
		cfg.Model = selectedModel

		keys, _ := config.LoadAPIKeys(flagKeysFile)
		accounts, _ := auth.ListAccounts()

		if len(keys) == 0 && len(accounts) == 0 {
			term.LogError("Nenhuma chave de API ou conta encontrada.\n" +
				"  • Instale o 'agy' para usar sua assinatura Gemini Pro sem restrições.\n" +
				"  • Ou configure GEMINI_API_KEY no ~/.env.")
			os.Exit(1)
		}

		keyPool, err := pool.NewAuthPool(keys, accounts)
		if err != nil {
			term.LogError("Falha ao inicializar AuthPool: %v", err)
			os.Exit(1)
		}

		apiClient := client.NewGeminiClient(keyPool, cfg.MaxRetries, cfg.Timeout)
		runner = engine.NewGeminiClientRunner(apiClient)
		term.LogInfo("⚡ Engine ATIVA: Gemini REST API | Credenciais: %d | Workers: %d",
			keyPool.Size(), cfg.Concurrency)
	}

	// 2. Setup Orchestrator
	orch := engine.NewOrchestrator(cfg, runner, configuredAccounts, term)

	// 3. Graceful Signal Handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		term.LogWarn("Sinal de interrupção recebido. Finalizando tarefas...")
		cancel()
	}()

	remainingArgs := fs.Args()

	// 4. Execution Mode Branching
	switch mode {
	case "create":
		prompt := ""
		if len(remainingArgs) > 0 {
			prompt = remainingArgs[0]
		}
		stdinData, hasStdin, _ := engine.ReadAllStdin()
		if prompt == "" && hasStdin && stdinData != "" {
			prompt = stdinData
		}
		if prompt == "" {
			term.LogError("Uso: overclock create [FLAGS] 'PROMPT'")
			os.Exit(1)
		}

		outDir := flagOutDir
		if outDir == "" {
			outDir = "./generated-project"
		}

		var contextFiles []string
		if flagContext != "" {
			contextFiles = append(contextFiles, flagContext)
		}
		if flagFrom != "" {
			contextFiles = append(contextFiles, flagFrom)
		}

		lifecycle := engine.LifecycleOptions{
			InstallDeps: flagInstall,
			VerifyBuild: flagVerify,
			InitGit:     flagGit,
		}

		if err := orch.RunCreateProject(ctx, prompt, outDir, contextFiles, lifecycle); err != nil {
			term.LogError("Erro na criação do projeto: %v", err)
			os.Exit(1)
		}

	case "resume":
		projectDir := "."
		if len(remainingArgs) > 0 {
			projectDir = remainingArgs[0]
		} else if flagOutDir != "" {
			projectDir = flagOutDir
		}

		lifecycle := engine.LifecycleOptions{
			InstallDeps: flagInstall,
			VerifyBuild: flagVerify,
			InitGit:     flagGit,
		}

		if err := orch.RunResumeProject(ctx, projectDir, lifecycle); err != nil {
			term.LogError("Erro ao retomar projeto: %v", err)
			os.Exit(1)
		}

	case "map":
		if len(remainingArgs) == 0 {
			term.LogError("Uso: overclock map [FLAGS] 'PROMPT' [ARQUIVOS...]")
			os.Exit(1)
		}
		prompt := remainingArgs[0]
		files := remainingArgs[1:]

		if len(files) == 0 {
			stdinData, hasStdin, _ := engine.ReadAllStdin()
			if hasStdin && strings.TrimSpace(stdinData) != "" {
				for _, line := range strings.Split(stdinData, "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						matches, err := filepath.Glob(line)
						if err == nil && len(matches) > 0 {
							files = append(files, matches...)
						} else {
							files = append(files, line)
						}
					}
				}
			}
		}

		if len(files) == 0 {
			term.LogError("Nenhum arquivo fornecido para o modo map.")
			os.Exit(1)
		}

		if err := orch.RunMapFiles(ctx, prompt, files); err != nil {
			term.LogError("Erro no modo map: %v", err)
			os.Exit(1)
		}

	case "lines":
		prompt := ""
		if len(remainingArgs) > 0 {
			prompt = remainingArgs[0]
		}
		if err := orch.RunMapLines(ctx, prompt, os.Stdin); err != nil {
			term.LogError("Erro no modo lines: %v", err)
			os.Exit(1)
		}

	case "pipe":
		fallthrough
	default:
		prompt := ""
		if len(remainingArgs) > 0 {
			prompt = remainingArgs[0]
		}

		stdinData, _, err := engine.ReadAllStdin()
		if err != nil {
			term.LogError("Erro ao ler STDIN: %v", err)
			os.Exit(1)
		}

		if prompt == "" && stdinData == "" {
			printUsage()
			os.Exit(1)
		}

		// If out-dir is set, route automatically to project creation
		if flagOutDir != "" {
			if prompt == "" && stdinData != "" {
				prompt = stdinData
			}
			var contextFiles []string
			if flagContext != "" {
				contextFiles = append(contextFiles, flagContext)
			}
			if flagFrom != "" {
				contextFiles = append(contextFiles, flagFrom)
			}
			lifecycle := engine.LifecycleOptions{
				InstallDeps: flagInstall,
				VerifyBuild: flagVerify,
				InitGit:     flagGit,
			}
			if err := orch.RunCreateProject(ctx, prompt, flagOutDir, contextFiles, lifecycle); err != nil {
				term.LogError("%v", err)
				os.Exit(1)
			}
			return
		}

		if err := orch.RunPipe(ctx, prompt, stdinData); err != nil {
			term.LogError("%v", err)
			os.Exit(1)
		}
	}
}
