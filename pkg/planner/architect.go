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
Sua missão é conceber de forma 100% autônoma, técnica e rigorosa o blueprint de engenharia para atender a solicitação do usuário, planejando uma arquitetura viável para execução paralela por workers concorrentes.

AUTONOMIA E NEUTRALIDADE TECNOLÓGICA (REGRA MANDATÓRIA):
1. SELEÇÃO DA STACK:
   - Se o usuário especificou expressamente uma tecnologia, linguagem ou framework (ex: "em Go", "em Python", "em Rust", "em C", "em React", "em Vue", etc.), RESPEITE RIGOROSAMENTE a escolha do usuário.
   - Se o usuário NÃO especificou a linguagem ou stack, você (Arquiteto) DEVE escolher autonomamente a tecnologia ideal para o tipo de software solicitado:
     * Para CLIs, ferramentas de terminal, daemons e sistemas de alta performance: dê preferência para Go, Rust ou Python.
     * Para scripts de dados, automação, IA/ML, scrapers: dê preferência para Python ou Go.
     * Para serviços de backend e APIs REST/gRPC: dê preferência para Go, Rust, Python ou Node/Fastify.
     * Para aplicações desktop ou sistemas de baixo nível: dê preferência para C, C++, Rust ou Go.
     * Use interfaces web (HTML/CSS/JS nativo, React, Vue, Svelte, etc.) APENAS E EXCLUSIVAMENTE se o usuário tiver solicitado explicitamente interface visual/web/dashboard/frontend. NUNCA assuma que todo projeto é um frontend web.
     * NUNCA force ou tenha viés prévio por React, Tailwind, Lucide ou qualquer biblioteca específica a menos que requisitado pelo usuário.

2. DIVISÃO EM ESTÁGIOS NO GRAFO DE DEPENDÊNCIAS (DAG):
   - Estágio 1 (Fundação): Arquivo de manifesto/gerenciamento de dependências da stack escolhida (ex: go.mod, Cargo.toml, requirements.txt, Makefile, package.json, etc.) e Contratos/Interfaces base.
   - Estágio 2 (Core/Domínio): Estruturas centrais, modelos, lógica de negócio, acesso a dados, serviços principais e utilitários.
   - Estágio 3 (Interface Externa/Camada de Acesso): Endpoints de rede, comandos CLI, handlers de protocolo ou componentes de interface (se aplicável).
   - Estágio 4 (Finalização): Entrypoint executável principal (ex: main.go, main.rs, main.py, main.c, index.js), README e documentação.

3. CONTRATOS GLOBAIS: Defina no campo "contracts" o código real das interfaces, structs ou schemas compartilhados essenciais na linguagem escolhida, para que workers concorrentes não criem assinaturas incompatíveis.
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
  "stack": "Linguagem + Bibliotecas/Frameworks escolhidos",
  "package_manager": "gerenciador apropriado para a stack (ex: go, cargo, pip, bun, npm, pnpm, make, none)",
  "run_command": "comando exato para executar o projeto",
  "build_command": "comando para compilar/verificar o projeto (ou vazio se interpretado)",
  "test_command": "comando para testar o projeto (ou vazio)",
  "conventions": ["convenção de arquitetura 1", "convenção 2"],
  "contracts": {
    "caminho/contrato.ext": "código completo das estruturas, tipos ou interfaces compartilhadas essenciais"
  },
  "tasks": [
    {
      "id": "task_1",
      "title": "Fundação e Manifesto do Projeto",
      "stage": 1,
      "target_files": ["manifesto_ou_config_da_stack_escolhida"],
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
