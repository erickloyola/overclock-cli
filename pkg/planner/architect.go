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
	TechDecision   string            `json:"tech_decision"`
	Stack          string            `json:"stack"`
	PackageManager string            `json:"package_manager"`
	RunCommand     string            `json:"run_command"`
	BuildCommand   string            `json:"build_command"`
	TestCommand    string            `json:"test_command"`
	Conventions    []string          `json:"conventions"`
	Contracts      map[string]string `json:"contracts"` // "caminho/contrato.ext" -> code
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
	concurrency int,
) (*memory.Blackboard, error) {
	systemPrompt := `Você é o Arquiteto de Software Chefe do Overclock.
Sua missão é conceber de forma 100% autônoma, técnica e rigorosa o blueprint de engenharia para atender a solicitação do usuário, planejando uma arquitetura viável para execução paralela por workers concorrentes.

PRINCÍPIO FUNDAMENTAL: SELEÇÃO DA MELHOR TECNOLOGIA DE ENGENHARIA (ZERO VIÉS JS/TS):
Você é um Engenheiro de Software Poliglota e pragmático. Você NÃO DEVE ter viés automático por TypeScript, JavaScript, Node.js, React ou Tailwind. Sua função é avaliar com rigor técnico qual é a tecnologia IDEAL da indústria para o domínio do software solicitado.

1. RESPEITO ABSOLUTO À ESCOLHA EXPRESSA DO USUÁRIO:
   - Se o usuário especificou expressamente uma tecnologia, linguagem ou framework (ex: "em Go", "em Python", "em Rust", "em C", "em React", "em Vue", etc.), RESPEITE RIGOROSAMENTE a escolha do usuário.

2. QUANDO O USUÁRIO NÃO ESPECIFICOU A STACK (SELEÇÃO AUTÔNOMA POR ENGENHARIA DE DOMÍNIO):
   Analise a categoria e os requisitos não funcionais do software e selecione obrigatoriamente a tecnologia mais adequada:

   • Ferramentas de Linha de Comando (CLIs), Daemons de Sistema, Ferramentas de Terminal e TUI:
     - ESCOLHA: Go (ex: Cobra, Bubbletea, Lipgloss) ou Rust (ex: Clap, Ratatui) ou Python (ex: Click, Typer, Rich).
     - JUSTIFICATIVA DE ENGENHARIA: Ferramentas de terminal exigem inicialização instantânea (<10ms), compilação estática em binário único executável sem dependência de runtime externo (Node/V8), baixo consumo de memória RAM e facilidade de distribuição sem pasta node_modules pesada.
     - PROIBIÇÃO EXPRESSA: NUNCA crie CLIs em Node.js / JavaScript / TypeScript a menos que o usuário exija expressamente.

   • Microsserviços de Backend, APIs REST, Serviços de Rede e Alta Concorrência:
     - ESCOLHA: Go (ex: Gin, Fiber, Chi, net/http nativo) ou Rust (ex: Axum, Actix-web) ou Python (ex: FastAPI).
     - JUSTIFICATIVA DE ENGENHARIA: Concorrência nativa via goroutines/async channels, alto throughput de I/O de rede e robustez de tipagem em runtime compilado.

   • Automação, Scripts de Dados, Web Scraping, IA, Machine Learning e Processamento de Texto:
     - ESCOLHA: Python (uv/pip, httpx, beautifulsoup4, pandas, typer) ou Go.
     - JUSTIFICATIVA DE ENGENHARIA: Ecossistema dominante, bibliotecas padrão ricas e agilidade de escrita.

   • Aplicações de Baixo Nível, Computação de Alta Performance, Drivers, Embutidos:
     - ESCOLHA: C, C++ ou Rust.

   • Aplicações Web, Dashboards no Navegador e Frontends Visuais:
     - ESCOLHA: Tecnologias web (HTML/CSS/JS nativo, React, Vue, Svelte, Tailwind).
     - REGRA: Use tecnologias web APENAS E EXCLUSIVAMENTE se o usuário tiver solicitado explicitamente interface visual no navegador, web app, frontend ou dashboard web. NUNCA assuma que todo projeto é um frontend web.

3. DIVISÃO EM ESTÁGIOS NO GRAFO DE DEPENDÊNCIAS (DAG) E MAXIMIZAÇÃO DE PARALELISMO:
   - Estágio 1 (Fundação): Arquivo de manifesto/gerenciamento de dependências da stack escolhida (ex: go.mod, Cargo.toml, pyproject.toml / requirements.txt, Makefile, package.json para web) e Contratos/Interfaces base.
   - Estágio 2 (Core/Domínio): Estruturas centrais, modelos, lógica de negócio, acesso a dados, serviços principais e utilitários.
   - Estágio 3 (Interface Externa/Camada de Acesso): Endpoints de rede, comandos CLI, handlers de protocolo ou componentes de interface (se aplicável).
   - Estágio 4 (Finalização & Testes Automatizados): Entrypoint executável principal (ex: main.go, main.rs, main.py, main.c, index.html/main.ts), README e OBRIGATORIAMENTE suíte de testes unitários automatizados cobrindo os módulos centrais e regras de negócio do Estágio 2 (ex: *_test.go em Go, *.test.ts em TS/Node, test_*.py em Python, tests/*.rs em Rust).

   • DECOMPOSIÇÃO BASEADA EM WORKERS (JOBS-AWARE PARALLELISM):
     O Overclock é um motor de aceleração massiva em enxame com N workers concorrentes (ex: 4, 8, 10 workers).
     Decomponha os módulos e serviços do projeto em tarefas granulares e independentes para que cada estágio (especialmente Estágio 2 e Estágio 3) contenha múltiplos nós paralelos no DAG, visando atingir até a capacidade máxima de workers alocada.
     Evite agrupar arquivos distintos ou heterogêneos na mesma tarefa quando puderem ser paralelizados por workers diferentes. Cada tarefa deve focar preferencialmente em 1 a 2 arquivos coesos.
     Tarefas do mesmo estágio que rodam em paralelo não devem depender entre si, apenas de contratos e artefatos de estágios anteriores.

4. CONTRATOS GLOBAIS: Defina no campo "contracts" o código real das interfaces, structs ou schemas compartilhados essenciais na linguagem escolhida, para que workers concorrentes não criem assinaturas incompatíveis.
5. JUSTIFICATIVA TÉCNICA OBRIGATÓRIA: Preencha o campo "tech_decision" explicando por que a stack escolhida é tecnicamente a melhor para os requisitos do projeto.
6. FORMATO DE SAÍDA: Responda ESTRITAMENTE com um objeto JSON válido (sem texto antes ou depois, sem markdown fora do JSON).`

	var promptBuilder strings.Builder
	promptBuilder.WriteString("SOLICITAÇÃO DO PROJETO:\n")
	promptBuilder.WriteString(prompt)
	promptBuilder.WriteString("\n\n")

	if concurrency > 0 {
		promptBuilder.WriteString(fmt.Sprintf("CAPACIDADE DE CONCORRÊNCIA DO ENXAME: %d workers paralelos disponíveis.\n", concurrency))
		promptBuilder.WriteString(fmt.Sprintf("DIRETRIZ DE ESCALABILIDADE: Decomponha o projeto de forma granular para aproveitar a concorrência de até %d workers simultâneos por estágio (especialmente nos Estágios 2 e 3).\n\n", concurrency))
	}

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
  "tech_decision": "justificativa técnica detalhada explicando por que esta linguagem e stack são a melhor escolha de engenharia para este software",
  "stack": "Linguagem + Bibliotecas/Frameworks escolhidos",
  "package_manager": "gerenciador apropriado para a stack (ex: go, cargo, uv, pip, make, none - ou npm/bun apenas para web)",
  "run_command": "comando exato para executar o projeto",
  "build_command": "comando para compilar/verificar o projeto (ou vazio se interpretado)",
  "test_command": "comando para testar o projeto (ou vazio)",
  "conventions": ["convenção de arquitetura 1", "convenção 2"],
  "contracts": {
    "caminho/contrato.ext": "código completo das estruturas, tipos ou interfaces compartilhadas essenciais na linguagem escolhida"
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
		TechDecision:   rawPlan.TechDecision,
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
