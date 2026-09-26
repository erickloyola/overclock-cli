package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"overclock/pkg/engine"
	"overclock/pkg/hub"
	"overclock/pkg/hyprland"
	"overclock/pkg/memory"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 message.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 message.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError defines a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolDefinition defines an MCP tool.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// Server is the Model Context Protocol server exposing Overclock memory and Hyprland cockpit panes.
type Server struct {
	projectDir  string
	bb          *memory.Blackboard
	stdin       io.Reader
	stdout      io.Writer
	hub         *hub.Hub
	hyprland    *hyprland.Controller
	worktreeMgr *engine.WorktreeManager
	worktrees   map[string]*engine.Worktree
	mu          sync.Mutex
}

// NewServer creates an MCP server bound to a specific project directory.
func NewServer(projectDir string, stdin io.Reader, stdout io.Writer) *Server {
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if projectDir == "" {
		projectDir = "."
	}

	stateFile := filepath.Join(projectDir, ".overclock", "state.json")
	bb, err := memory.LoadState(stateFile)
	if err != nil || bb == nil {
		bb = memory.NewBlackboard()
	}

	return &Server{
		projectDir: projectDir,
		bb:         bb,
		stdin:      stdin,
		stdout:     stdout,
		hyprland:   hyprland.NewController(),
		worktrees:  make(map[string]*engine.Worktree),
	}
}

// Close releases resources held by the MCP server (Hub socket, worktrees).
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hub != nil {
		_ = s.hub.Close()
	}
	if s.hyprland != nil {
		_ = s.hyprland.SetLayout("scrolling")
	}
}

func (s *Server) ensureHub() (*hub.Hub, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hub == nil {
		h := hub.NewHub()
		if err := h.Start(); err != nil {
			return nil, err
		}
		s.hub = h
	}
	return s.hub, nil
}

func (s *Server) ensureWorktreeMgr() (*engine.WorktreeManager, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.worktreeMgr == nil {
		wm, err := engine.NewWorktreeManager(s.projectDir)
		if err != nil {
			return nil, err
		}
		s.worktreeMgr = wm
	}
	return s.worktreeMgr, nil
}

// Serve runs the JSON-RPC stdio loop.
func (s *Server) Serve() error {
	defer s.Close()

	scanner := bufio.NewScanner(s.stdin)
	// Allow large messages up to 10MB
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		s.handleRequest(&req)
	}

	return scanner.Err()
}

func (s *Server) handleRequest(req *JSONRPCRequest) {
	// Notifications (no ID)
	if req.ID == nil {
		return
	}

	switch req.Method {
	case "initialize":
		s.sendResult(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    "overclock-mcp",
				"version": "2.0.0",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{"listChanged": false},
			},
		})

	case "tools/list":
		s.sendResult(req.ID, map[string]interface{}{
			"tools": s.getToolDefinitions(),
		})

	case "tools/call":
		s.handleToolCall(req)

	case "ping":
		s.sendResult(req.ID, map[string]string{"status": "pong"})

	default:
		s.sendError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) getToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		// Memory & Blackboard Tools
		{
			Name:        "mcp__overclock__memory_read",
			Description: "Lê um fato compartilhado, arquivo, contrato ou lição da memória do Overclock",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]string{
						"type":        "string",
						"description": "Chave do fato, nome do arquivo ou nome do contrato",
					},
					"type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"fact", "file", "contract"},
						"description": "Tipo de item a consultar (padrão: fact)",
					},
				},
				"required": []string{"key"},
			},
		},
		{
			Name:        "mcp__overclock__memory_write",
			Description: "Grava um novo fato operacional na memória compartilhada do Overclock (OverMemory)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"CONFIG", "CONVENTION", "DEP", "RUNTIME", "GENERAL"},
						"description": "Categoria do fato operacional",
					},
					"key": map[string]string{
						"type":        "string",
						"description": "Identificador da chave do fato",
					},
					"value": map[string]string{
						"type":        "string",
						"description": "Valor ou descrição do fato",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"project", "global"},
						"description": "Escopo da gravação (projeto atual ou global para todos os projetos)",
					},
				},
				"required": []string{"category", "key", "value"},
			},
		},
		{
			Name:        "mcp__overclock__memory_list_facts",
			Description: "Lista todos os fatos operacionais gravados na memória do projeto",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"category": map[string]string{
						"type":        "string",
						"description": "Filtro opcional por categoria (CONFIG, CONVENTION, DEP, RUNTIME, GENERAL)",
					},
				},
			},
		},
		{
			Name:        "mcp__overclock__memory_get_lessons",
			Description: "Lista lições aprendidas e erros prevenidos detectados pelo compilador e supervisor",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},
		{
			Name:        "mcp__overclock__get_status",
			Description: "Retorna o status geral do projeto, manifesto, arquivos e tarefas do DAG",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},

		// Hyprland Cockpit & Multi-Pane Orchestration Tools
		{
			Name:        "mcp__overclock__pane_spawn",
			Description: "Dispara uma nova janela de terminal no Hyprland (ex: Kitty) isolada em uma Git Worktree dedicada para executar uma tarefa em paralelo (Maestro Framework)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]string{
						"type":        "string",
						"description": "Identificador único da tarefa/worker (ex: worker-backend, feat-auth)",
					},
					"task": map[string]string{
						"type":        "string",
						"description": "Descrição e objetivo direto da tarefa (Clean Context)",
					},
					"role": map[string]string{
						"type":        "string",
						"description": "Papel do agente especialista (ex: Backend Go Developer)",
					},
					"contracts": map[string]string{
						"type":        "string",
						"description": "Especificação formal de tipos, structs e contratos de API (Gate 1)",
					},
					"preset": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"agy", "claude", "gemini", "bash", "overclock-ui"},
						"description": "Preset do agente local (padrão: agy)",
					},
					"terminal": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"kitty", "foot", "alacritty", "ghostty"},
						"description": "Emulador de terminal preferido (padrão: kitty)",
					},
					"workspace": map[string]interface{}{
						"type":        "string",
						"description": "ID ou nome do workspace do Hyprland onde abrir o pane (padrão: mesmo workspace do Maestro)",
					},
					"silent": map[string]interface{}{
						"type":        "boolean",
						"description": "Se verdadeiro, abre o pane em segundo plano sem mudar o foco da tela (padrão: false)",
					},
					"use_worktree": map[string]interface{}{
						"type":        "boolean",
						"description": "Se deve provisionar uma Git Worktree isolada em .worktrees/ (padrão: true)",
					},
				},
				"required": []string{"name", "task"},
			},
		},
		{
			Name:        "mcp__overclock__pane_wait",
			Description: "Aguarda de forma reativa por evento (Zero Polling) a conclusão e submissão de handoff de um worker no Hyprland",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]string{
						"type":        "string",
						"description": "Identificador do worker a aguardar",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "number",
						"description": "Timeout limite em segundos (padrão: 300)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "mcp__overclock__pane_write",
			Description: "Envia uma mensagem ou instrução complementar para um worker conectado via IPC Socket",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]string{
						"type":        "string",
						"description": "Identificador do worker",
					},
					"message": map[string]string{
						"type":        "string",
						"description": "Mensagem ou instrução a ser enviada",
					},
				},
				"required": []string{"name", "message"},
			},
		},
		{
			Name:        "mcp__overclock__handoff_submit",
			Description: "Submete o relatório formal de handoff de uma tarefa concluída",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]string{
						"type":        "string",
						"description": "Identificador do worker",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"PASS", "FAIL"},
						"description": "Status de conclusão dos testes e critérios",
					},
					"artifacts": map[string]interface{}{
						"type": "array",
						"items": map[string]string{
							"type": "string",
						},
						"description": "Lista dos caminhos dos arquivos produzidos ou modificados",
					},
					"notes": map[string]string{
						"type":        "string",
						"description": "Notas e resumo objetivo da entrega para o Maestro",
					},
				},
				"required": []string{"name", "status"},
			},
		},
		{
			Name:        "mcp__overclock__pane_dismiss",
			Description: "Fecha a janela do worker no Hyprland, valida Gate e realiza merge/limpeza da Git Worktree (Auto-Teardown)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]string{
						"type":        "string",
						"description": "Identificador do worker a encerrar",
					},
					"merge": map[string]interface{}{
						"type":        "boolean",
						"description": "Se deve realizar o merge git na branch principal (padrão: true)",
					},
					"cleanup": map[string]interface{}{
						"type":        "boolean",
						"description": "Se deve remover o diretório da worktree (padrão: true)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "mcp__overclock__pane_list",
			Description: "Lista todos os panes e workers ativos no Hyprland com status de conexão e handoff",
			InputSchema: map[string]interface{}{
				"type": "object",
			},
		},
	}
}

func (s *Server) handleToolCall(req *JSONRPCRequest) {
	var call struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		s.sendError(req.ID, -32602, "Invalid params")
		return
	}

	var outputText string

	switch call.Name {
	case "mcp__overclock__memory_read":
		key, _ := call.Arguments["key"].(string)
		itemType, _ := call.Arguments["type"].(string)
		if itemType == "" {
			itemType = "fact"
		}

		switch itemType {
		case "fact":
			if fact, ok := s.bb.GetFact(key); ok {
				outputText = fmt.Sprintf("[%s] %s: %s (fonte: %s, criado: %s)",
					fact.Category, fact.Key, fact.Value, fact.Source, fact.CreatedAt.Format("15:04:05"))
			} else {
				outputText = fmt.Sprintf("Fato '%s' não encontrado na memória do projeto.", key)
			}
		case "file":
			if f, ok := s.bb.GetFile(key); ok {
				outputText = fmt.Sprintf("Arquivo: %s (v%d, %d bytes)\n\n%s", f.Path, f.Version, f.Bytes, f.Content)
			} else {
				outputText = fmt.Sprintf("Arquivo '%s' não encontrado na memória.", key)
			}
		case "contract":
			contracts := s.bb.GetContracts()
			if c, ok := contracts[key]; ok {
				outputText = fmt.Sprintf("Contrato: %s\n\n%s", key, c)
			} else {
				outputText = fmt.Sprintf("Contrato '%s' não encontrado.", key)
			}
		default:
			outputText = fmt.Sprintf("Tipo '%s' desconhecido. Use fact, file ou contract.", itemType)
		}

	case "mcp__overclock__memory_write":
		catStr, _ := call.Arguments["category"].(string)
		key, _ := call.Arguments["key"].(string)
		val, _ := call.Arguments["value"].(string)
		scope, _ := call.Arguments["scope"].(string)

		category := memory.FactCategory(strings.ToUpper(catStr))
		fact := s.bb.RecordFact(category, key, val, "MCP-Agent")
		_ = memory.AppendEvent(s.projectDir, memory.EventFactRecorded, fact)
		_ = s.bb.SaveState(s.projectDir)

		if scope == "global" {
			if gm, err := memory.LoadGlobalMemory(); err == nil {
				gm.SetFact(category, key, val, "MCP-Agent")
				_ = gm.Save()
			}
			outputText = fmt.Sprintf("✅ Fato [%s] '%s' registrado na memória do projeto e na memória global!", category, key)
		} else {
			outputText = fmt.Sprintf("✅ Fato [%s] '%s' registrado na memória compartilhada do projeto.", category, key)
		}

	case "mcp__overclock__memory_list_facts":
		catFilter, _ := call.Arguments["category"].(string)
		var facts []*memory.SharedFact
		if catFilter != "" {
			facts = s.bb.GetFactsByCategory(memory.FactCategory(strings.ToUpper(catFilter)))
		} else {
			facts = s.bb.GetFacts()
		}

		if len(facts) == 0 {
			outputText = "Nenhum fato operacional registrado na memória deste projeto."
		} else {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("📋 Fatos Compartilhados Registrados (%d):\n", len(facts)))
			for _, f := range facts {
				sb.WriteString(fmt.Sprintf("• [%s] %s: %s (fonte: %s)\n", f.Category, f.Key, f.Value, f.Source))
			}
			outputText = sb.String()
		}

	case "mcp__overclock__memory_get_lessons":
		lessons := s.bb.GetLessons()
		if len(lessons) == 0 {
			outputText = "Nenhuma lição aprendida ou falha registrada até o momento."
		} else {
			outputText = s.bb.LessonsSummary()
		}

	case "mcp__overclock__get_status":
		manifest := s.bb.GetManifest()
		files := s.bb.GetAllFiles()
		tasks := s.bb.GetAllTasks()
		readyTasks := s.bb.GetReadyTasks()

		var sb strings.Builder
		sb.WriteString("⚡ OVERCLOCK PROJECT STATUS\n")
		sb.WriteString(fmt.Sprintf("Nome: %s\nStack: %s\nGerenciador: %s\nComando: %s\n\n",
			manifest.Name, manifest.Stack, manifest.PackageManager, manifest.RunCommand))
		sb.WriteString(fmt.Sprintf("Arquivos na memória: %d\n", len(files)))
		sb.WriteString(fmt.Sprintf("Total de tarefas no DAG: %d (Prontas: %d)\n", len(tasks), len(readyTasks)))
		outputText = sb.String()

	case "mcp__overclock__pane_spawn":
		name, _ := call.Arguments["name"].(string)
		task, _ := call.Arguments["task"].(string)
		role, _ := call.Arguments["role"].(string)
		contracts, _ := call.Arguments["contracts"].(string)
		preset, _ := call.Arguments["preset"].(string)
		terminalStr, _ := call.Arguments["terminal"].(string)
		workspaceStr, _ := call.Arguments["workspace"].(string)
		silentVal, _ := call.Arguments["silent"].(bool)
		useWorktreeVal, hasWorktree := call.Arguments["use_worktree"].(bool)
		useWorktree := true
		if hasWorktree {
			useWorktree = useWorktreeVal
		}

		if name == "" || task == "" {
			s.sendError(req.ID, -32602, "Campos 'name' e 'task' são obrigatórios")
			return
		}

		if preset == "" {
			preset = "agy"
		}

		targetDir := s.projectDir
		var branch string

		// 1. Git Worktree Isolation
		if useWorktree {
			wm, err := s.ensureWorktreeMgr()
			if err != nil {
				s.sendError(req.ID, -32000, fmt.Sprintf("Falha ao inicializar WorktreeManager: %v", err))
				return
			}
			wt, err := wm.CreateWorktree(name)
			if err != nil {
				s.sendError(req.ID, -32000, fmt.Sprintf("Falha ao criar Git Worktree: %v", err))
				return
			}
			s.mu.Lock()
			s.worktrees[name] = wt
			s.mu.Unlock()
			targetDir = wt.Path
			branch = wt.Branch
		}

		// 2. Hub Registration
		h, err := s.ensureHub()
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Falha ao inicializar Hub IPC: %v", err))
			return
		}

		h.RegisterWorker(hub.WorkerInitPayload{
			WorkerID:  name,
			Role:      role,
			Task:      task,
			Contracts: contracts,
			Worktree:  targetDir,
			Branch:    branch,
			Preset:    preset,
		})

		// 3. Spawning Window in Hyprland
		termEnum := hyprland.TerminalKitty
		if terminalStr != "" {
			termEnum = hyprland.TerminalEmulator(terminalStr)
		}

		targetWs := workspaceStr
		if targetWs == "" || targetWs == "current" {
			targetWs = s.hyprland.GetCallerWorkspace()
		}

		workerCmd := fmt.Sprintf("overclock worker --id %s --sock %s || { echo \"\\n[Erro na execução do worker]\"; read -p \"Pressione ENTER para fechar...\" -r; }", name, h.SocketPath())
		windowTitle := fmt.Sprintf("⚡ Overclock: %s", name)

		err = s.hyprland.SpawnWindow(hyprland.WindowOptions{
			Class:     "overclock-worker",
			Title:     windowTitle,
			Directory: targetDir,
			Command:   workerCmd,
			Terminal:  termEnum,
			Workspace: targetWs,
			Silent:    silentVal,
		})
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Falha ao abrir janela no Hyprland: %v", err))
			return
		}

		outputText = fmt.Sprintf("⚡ Pane disparado com sucesso!\n• Worker: %s\n• Diretório: %s\n• Branch: %s\n• Workspace: %s\n• Janela: '%s'\n• Preset: %s",
			name, targetDir, branch, targetWs, windowTitle, preset)

	case "mcp__overclock__pane_wait":
		name, _ := call.Arguments["name"].(string)
		timeoutSec, _ := call.Arguments["timeout_seconds"].(float64)
		if timeoutSec <= 0 {
			timeoutSec = 300
		}

		h, err := s.ensureHub()
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Hub não disponível: %v", err))
			return
		}

		timeout := time.Duration(timeoutSec) * time.Second
		handoff, errWait := h.WaitForHandoff(name, timeout)
		if errWait != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Erro ao aguardar handoff de '%s': %v", name, errWait))
			return
		}

		// Grava entrega na memória compartilhada
		s.bb.RecordFact(memory.FactCategoryGeneral, fmt.Sprintf("handoff_%s", name),
			fmt.Sprintf("Status: %s, Arquivos: %s", handoff.Status, strings.Join(handoff.Artifacts, ", ")), name)
		_ = s.bb.SaveState(s.projectDir)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("⚡ [HANDOFF RECEBIDO: %s]\n", name))
		sb.WriteString(fmt.Sprintf("• Status: %s\n", handoff.Status))
		sb.WriteString(fmt.Sprintf("• Artefatos Produzidos (%d): %s\n", len(handoff.Artifacts), strings.Join(handoff.Artifacts, ", ")))
		sb.WriteString(fmt.Sprintf("• Notas de Entrega: %s\n", handoff.Notes))
		outputText = sb.String()

	case "mcp__overclock__pane_write":
		name, _ := call.Arguments["name"].(string)
		message, _ := call.Arguments["message"].(string)

		h, err := s.ensureHub()
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Hub não disponível: %v", err))
			return
		}

		err = h.SendToWorker(name, hub.MsgTypeWrite, message)
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Falha ao enviar mensagem para worker '%s': %v", name, err))
			return
		}
		outputText = fmt.Sprintf("Mensagem enviada com sucesso para worker '%s'.", name)

	case "mcp__overclock__handoff_submit":
		name, _ := call.Arguments["name"].(string)
		status, _ := call.Arguments["status"].(string)
		notes, _ := call.Arguments["notes"].(string)
		var artifacts []string
		if rawArr, ok := call.Arguments["artifacts"].([]interface{}); ok {
			for _, item := range rawArr {
				if s, ok := item.(string); ok {
					artifacts = append(artifacts, s)
				}
			}
		}

		h, err := s.ensureHub()
		if err != nil {
			s.sendError(req.ID, -32000, fmt.Sprintf("Hub não disponível: %v", err))
			return
		}

		h.SubmitHandoff(name, &hub.HandoffPayload{
			Status:    status,
			Artifacts: artifacts,
			Notes:     notes,
		})
		outputText = fmt.Sprintf("✅ Handoff de '%s' registrado com sucesso (Status: %s).", name, status)

	case "mcp__overclock__pane_dismiss":
		name, _ := call.Arguments["name"].(string)
		mergeVal, hasMerge := call.Arguments["merge"].(bool)
		merge := true
		if hasMerge {
			merge = mergeVal
		}
		cleanupVal, hasCleanup := call.Arguments["cleanup"].(bool)
		cleanup := true
		if hasCleanup {
			cleanup = cleanupVal
		}

		s.mu.Lock()
		wt, hasWt := s.worktrees[name]
		delete(s.worktrees, name)
		s.mu.Unlock()

		var results []string

		// 1. Signal dismiss to worker over IPC
		if s.hub != nil {
			_ = s.hub.SendToWorker(name, hub.MsgTypeDismiss, nil)
			s.hub.UnregisterWorker(name)
		}

		// 2. Git Merge if worktree exists
		if hasWt && s.worktreeMgr != nil {
			if merge {
				_ = s.worktreeMgr.CommitWorktree(wt, fmt.Sprintf("feat: finalize task %s", name))
				if errMerge := s.worktreeMgr.MergeWorktree(wt); errMerge != nil {
					results = append(results, fmt.Sprintf("⚠️ Falha no merge git: %v", errMerge))
				} else {
					results = append(results, fmt.Sprintf("✅ Merge realizado com sucesso na branch %s!", wt.BaseBranch))
				}
			}

			if cleanup {
				_ = s.worktreeMgr.CleanupWorktree(wt)
				results = append(results, "🧹 Worktree removida do disco.")
			}
		}

		// 3. Close Hyprland window
		windowTitle := fmt.Sprintf("⚡ Overclock: %s", name)
		if errClose := s.hyprland.CloseWindowByTitle(windowTitle); errClose != nil {
			results = append(results, fmt.Sprintf("ℹ️ Janela no Hyprland: %v", errClose))
		} else {
			results = append(results, "🪟 Janela no Hyprland encerrada (Auto-Teardown concluído).")
		}

		outputText = fmt.Sprintf("⚡ Teardown de '%s':\n%s", name, strings.Join(results, "\n"))

	case "mcp__overclock__pane_list":
		h, _ := s.ensureHub()
		workers := []string{}
		if h != nil {
			workers = h.ListWorkers()
		}

		clients, _ := s.hyprland.ListClients()
		var overclockClients []hyprland.Client
		for _, c := range clients {
			if c.Class == "overclock-worker" || strings.Contains(c.Title, "Overclock") {
				overclockClients = append(overclockClients, c)
			}
		}

		var sb strings.Builder
		sb.WriteString("⚡ [PANES & WORKERS OVERCLOCK NO HYPRLAND]\n")
		sb.WriteString(fmt.Sprintf("Total de janelas ativas: %d\n", len(overclockClients)))
		for _, c := range overclockClients {
			sb.WriteString(fmt.Sprintf("• Janela: %s (PID: %d, Class: %s)\n", c.Title, c.PID, c.Class))
		}
		sb.WriteString(fmt.Sprintf("\nWorkers registrados no Hub IPC (%d):\n", len(workers)))
		for _, w := range workers {
			status := "Em execução"
			if handoff, ok := h.GetHandoff(w); ok {
				status = fmt.Sprintf("Handoff: %s (%d arquivos)", handoff.Status, len(handoff.Artifacts))
			}
			sb.WriteString(fmt.Sprintf("• %s: %s\n", w, status))
		}
		outputText = sb.String()

	default:
		s.sendError(req.ID, -32601, fmt.Sprintf("Tool not found: %s", call.Name))
		return
	}

	s.sendResult(req.ID, map[string]interface{}{
		"content": []map[string]string{
			{
				"type": "text",
				"text": outputText,
			},
		},
	})
}

func (s *Server) sendResult(id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	_, _ = s.stdout.Write(append(data, '\n'))
}

func (s *Server) sendError(id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = s.stdout.Write(append(data, '\n'))
}
