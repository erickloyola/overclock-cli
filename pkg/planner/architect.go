package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"overclock/pkg/client"
	"overclock/pkg/memory"
)

// Runner represents the LLM execution interface.
type Runner interface {
	Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error)
	Name() string
}

// RawProjectPlan matches the expected JSON structure from the LLM Architect.
type RawProjectPlan struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Stack          string            `json:"stack"`
	PackageManager string            `json:"package_manager"`
	RunCommand     string            `json:"run_command"`
	BuildCommand   string            `json:"build_command"`
	TestCommand    string            `json:"test_command"`
	Conventions    []string          `json:"conventions"`
	Contracts      map[string]string `json:"contracts"` // "types/index.ts" -> code
	Tasks          []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Stage       int      `json:"stage"` // 1: Foundation, 2: Core, 3: UI/Routes, 4: Finalize
		TargetFiles []string `json:"target_files"`
		DependsOn   []string `json:"depends_on"`
		Spec        string   `json:"spec"`
	} `json:"tasks"`
}

// PlanProject executes the Architect instance to create a structured project blueprint.
func PlanProject(
	ctx context.Context,
	runner Runner,
	prompt string,
	envSummary string,
	contextData string,
	model string,
) (*memory.Blackboard, error) {
	systemPrompt := `Você é o Arquiteto de Software Chefe do Overclock.
Sua missão é receber uma solicitação de criação de projeto de software e transformá-la em um blueprint de engenharia completo, estruturado e rigorosamente viável para execução paralela por múltiplos workers.

REGRAS DE ARQUITETURA:
1. Escolha ou respeite a stack solicitada. Seja poliglota: suporte Go, Python, TypeScript/React, Rust, etc.
2. Divida o projeto em estágios claros no Grafo de Dependências (DAG):
   - Estágio 1 (Fundação): Arquivos de configuração (package.json, go.mod, tsconfig.json) e Contratos de Tipos Globais.
   - Estágio 2 (Core/Lógica): Serviços centrais, banco de dados, repositórios, utilitários e state stores.
   - Estágio 3 (Apresentação/Rotas): Telas, componentes visuais, endpoints HTTP/gRPC.
   - Estágio 4 (Finalização): Entrypoint principal (main.go, App.tsx, index.html), README e scripts.
3. CONTRATOS GLOBAIS: Defina antecipadamente no campo "contracts" o código real das interfaces e tipos compartilhados essenciais, para que workers concorrentes não inventem tipos incompatíveis.
4. DEPENDÊNCIAS: Cada tarefa deve listar explicitamente seus "depends_on" (IDs de tarefas de estágios anteriores).
5. FORMATO DE SAÍDA: Responda ESTRITAMENTE com um objeto JSON válido (sem texto antes ou depois, sem markdown fora do JSON).`

	var promptBuilder strings.Builder
	promptBuilder.WriteString("SOLICITAÇÃO DO PROJETO:\n")
	promptBuilder.WriteString(prompt)
	promptBuilder.WriteString("\n\n")

	if envSummary != "" {
		promptBuilder.WriteString("AMBIENTE LOCAL DETECTADO:\n")
		promptBuilder.WriteString(envSummary)
		promptBuilder.WriteString("\n\n")
	}

	if contextData != "" {
		promptBuilder.WriteString("CONTEXTO LOCAL / SCHEMAS EXISTENTES:\n")
		promptBuilder.WriteString(contextData)
		promptBuilder.WriteString("\n\n")
	}

	promptBuilder.WriteString(`Gere o JSON no seguinte formato:
{
  "name": "nome-do-projeto",
  "description": "breve descrição",
  "stack": "Linguagem + Frameworks",
  "package_manager": "bun|pnpm|npm|go|cargo|pip",
  "run_command": "comando para rodar (ex: bun dev ou go run .)",
  "build_command": "comando para build (ex: bun build ou go build)",
  "test_command": "comando para testar",
  "conventions": ["convenção 1", "convenção 2"],
  "contracts": {
    "caminho/tipo.ts": "código completo dos tipos/interfaces compartilhadas"
  },
  "tasks": [
    {
      "id": "task_1",
      "title": "Fundação e Configurações Base",
      "stage": 1,
      "target_files": ["package.json", "tsconfig.json"],
      "depends_on": [],
      "spec": "Instruções exatas sobre o que esses arquivos devem conter."
    }
  ]
}`)

	res, err := runner.Execute(ctx, client.RequestOptions{
		Model:        model,
		SystemPrompt: systemPrompt,
		Prompt:       promptBuilder.String(),
		Stream:       false,
	})
	if err != nil {
		return nil, fmt.Errorf("falha ao executar instância Arquiteto: %w", err)
	}

	jsonStr := extractJSON(res.Text)
	var rawPlan RawProjectPlan
	if err := json.Unmarshal([]byte(jsonStr), &rawPlan); err != nil {
		return nil, fmt.Errorf("resposta do Arquiteto não é um JSON válido: %w\nResposta bruta: %s", err, truncate(res.Text, 400))
	}

	// Initialize and populate Blackboard
	bb := memory.NewBlackboard()
	bb.SetManifest(memory.ProjectManifest{
		Name:           rawPlan.Name,
		Description:    rawPlan.Description,
		Stack:          rawPlan.Stack,
		PackageManager: rawPlan.PackageManager,
		RunCommand:     rawPlan.RunCommand,
		BuildCommand:   rawPlan.BuildCommand,
		TestCommand:    rawPlan.TestCommand,
		Conventions:    rawPlan.Conventions,
	})

	for contractName, code := range rawPlan.Contracts {
		bb.SetContract(contractName, code)
	}

	for _, t := range rawPlan.Tasks {
		stage := t.Stage
		if stage <= 0 {
			stage = 1
		}
		bb.RegisterTask(memory.TaskNode{
			ID:          t.ID,
			Title:       t.Title,
			Stage:       stage,
			TargetFiles: t.TargetFiles,
			DependsOn:   t.DependsOn,
			Spec:        t.Spec,
			Status:      memory.StatusPending,
		})
	}

	return bb, nil
}

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)

	// Remove markdown fences like ```json ... ```
	reFence := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
	if matches := reFence.FindStringSubmatch(raw); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Look for first '{' and last '}'
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start != -1 && end != -1 && end > start {
		return raw[start : end+1]
	}

	return raw
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
