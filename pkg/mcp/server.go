package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// Server is the Model Context Protocol server exposing Overclock memory.
type Server struct {
	projectDir string
	bb         *memory.Blackboard
	stdin      io.Reader
	stdout     io.Writer
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
	}
}

// Serve runs the JSON-RPC stdio loop.
func (s *Server) Serve() error {
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
