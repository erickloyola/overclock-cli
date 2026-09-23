package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"overclock/pkg/client"
	"overclock/pkg/ui"
)

// SubTask represents a decomposed module or unit of work from a complex prompt.
type SubTask struct {
	Index   int
	Title   string
	Content string
}

// DecomposePrompt analyzes a prompt and splits it into logical subtasks.
func DecomposePrompt(prompt string, maxParts int) (string, []SubTask) {
	if maxParts <= 1 {
		return "", []SubTask{{Index: 0, Title: "Execução Completa", Content: prompt}}
	}

	lines := strings.Split(prompt, "\n")
	var preambleLines []string
	var sections []SubTask

	headerRegex := regexp.MustCompile(`^(?:#{1,4}\s+|\d+\.\s+|###\s+)(.+)`)
	var currentTitle string
	var currentBody []string

	inSections := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if headerRegex.MatchString(trimmed) && (strings.Contains(trimmed, "###") || strings.Contains(trimmed, "##") || strings.Contains(trimmed, "####") || strings.Contains(strings.ToLower(trimmed), "module") || strings.Contains(strings.ToLower(trimmed), "component")) {
			if !inSections {
				inSections = true
			} else if currentTitle != "" {
				sections = append(sections, SubTask{
					Index:   len(sections),
					Title:   currentTitle,
					Content: strings.TrimSpace(strings.Join(currentBody, "\n")),
				})
				currentBody = nil
			}
			currentTitle = trimmed
			continue
		}

		if !inSections {
			preambleLines = append(preambleLines, line)
		} else {
			currentBody = append(currentBody, line)
		}
	}

	if currentTitle != "" && len(currentBody) > 0 {
		sections = append(sections, SubTask{
			Index:   len(sections),
			Title:   currentTitle,
			Content: strings.TrimSpace(strings.Join(currentBody, "\n")),
		})
	}

	preamble := strings.TrimSpace(strings.Join(preambleLines, "\n"))

	// If prompt didn't have explicit markdown headers, split logically into maxParts aspects
	if len(sections) < 2 {
		sections = nil
		aspects := []struct {
			Title string
			Focus string
		}{
			{
				Title: "Arquitetura, Tipos e Estruturas de Dados",
				Focus: "Desenvolva toda a arquitetura base, interfaces TypeScript, definições de tipos, contratos de API e modelos de dados necessários.",
			},
			{
				Title: "Componentes Principais e Lógica de Negócio",
				Focus: "Desenvolva os componentes centrais, lógica de estado, interações do usuário e manipuladores principais de eventos.",
			},
			{
				Title: "Visualizações, Telemetria e Elementos de UI",
				Focus: "Desenvolva gráficos, painéis de status, tabelas, terminais em tempo real e estilização completa com Tailwind CSS.",
			},
			{
				Title: "Integração Final, Mocks e Utilitários",
				Focus: "Desenvolva utilitários auxiliares, gerador de dados mock, integração da página e exportação executável do projeto.",
			},
		}

		numParts := maxParts
		if numParts > len(aspects) {
			numParts = len(aspects)
		}
		for i := 0; i < numParts; i++ {
			sections = append(sections, SubTask{
				Index: i,
				Title: aspects[i].Title,
				Content: fmt.Sprintf("Foco prioritário: %s\n\nEspecificações originais do prompt:\n%s",
					aspects[i].Focus, prompt),
			})
		}
	}

	return preamble, sections
}

// ExecuteDecomposed runs subtasks concurrently using available workers and accounts.
func ExecuteDecomposed(
	ctx context.Context,
	runner Runner,
	prompt string,
	contextData string,
	systemPrompt string,
	model string,
	concurrency int,
	accounts []string,
	term *ui.Terminal,
) (*client.ExecutionResult, error) {
	preamble, subtasks := DecomposePrompt(prompt, concurrency)
	totalTasks := len(subtasks)

	if totalTasks <= 1 {
		// Single task fallback
		reqOpts := client.RequestOptions{
			Model:        model,
			SystemPrompt: systemPrompt,
			Prompt:       prompt,
			ContextData:  contextData,
			Stream:       false,
		}
		return runner.Execute(ctx, reqOpts)
	}

	term.LogInfo("⚡ OVERCLOCK DECOMPOSIÇÃO: %d sub-tarefas paralelas geradas para concorrência de %d workers", totalTasks, concurrency)
	if len(accounts) > 1 {
		term.LogInfo("🔄 Rotação de múltiplas contas ativa: %v", accounts)
	}

	type subResult struct {
		Index    int
		Title    string
		Result   *client.ExecutionResult
		Duration time.Duration
		Err      error
	}

	results := make([]subResult, totalTasks)
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	overallStart := time.Now()

	for i, task := range subtasks {
		wg.Add(1)
		go func(idx int, st SubTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			account := "default"
			if len(accounts) > 0 {
				account = accounts[idx%len(accounts)]
			}

			term.LogInfo("[Worker %d/%d] [%s] 🚀 Iniciando: %s", idx+1, totalTasks, account, st.Title)

			var taskPrompt strings.Builder
			if preamble != "" {
				taskPrompt.WriteString("[CONTEXTO DO PROJETO]\n")
				taskPrompt.WriteString(preamble)
				taskPrompt.WriteString("\n\n")
			}
			taskPrompt.WriteString(fmt.Sprintf("[TAREFA ESPECÍFICA: %s]\n", st.Title))
			taskPrompt.WriteString(st.Content)
			taskPrompt.WriteString("\n\nInstrução: Gere o código/conteúdo completo, modular e executável desta seção. Responda diretamente com o código e explicações, sem executar comandos de terminal.")

			t0 := time.Now()
			res, err := runner.Execute(ctx, client.RequestOptions{
				Model:        model,
				SystemPrompt: systemPrompt,
				Prompt:       taskPrompt.String(),
				ContextData:  contextData,
				Stream:       false,
			})
			dur := time.Since(t0)

			if err != nil {
				term.LogError("[Worker %d/%d] ❌ Falha em '%s': %v", idx+1, totalTasks, st.Title, err)
			} else {
				term.LogInfo("[Worker %d/%d] ✅ Concluído: %s (%d chars em %.2fs)", idx+1, totalTasks, st.Title, len(res.Text), dur.Seconds())
			}

			results[idx] = subResult{
				Index:    idx,
				Title:    st.Title,
				Result:   res,
				Duration: dur,
				Err:      err,
			}
		}(i, task)
	}

	wg.Wait()
	overallDuration := time.Since(overallStart)

	// Stitch output
	var stitched strings.Builder
	var totalSequentialDur time.Duration
	var totalPromptTokens int
	var totalCandTokens int

	for _, sr := range results {
		if sr.Err != nil {
			stitched.WriteString(fmt.Sprintf("\n\n### ⚠️ %s (Erro durante geração: %v)\n", sr.Title, sr.Err))
			continue
		}
		if sr.Result != nil {
			totalSequentialDur += sr.Duration
			totalPromptTokens += sr.Result.PromptTokens
			totalCandTokens += sr.Result.CandidatesTokens

			stitched.WriteString(fmt.Sprintf("\n\n<!-- ========================================== -->\n"))
			stitched.WriteString(fmt.Sprintf("<!-- SEÇÃO %d: %s -->\n", sr.Index+1, sr.Title))
			stitched.WriteString(fmt.Sprintf("<!-- ========================================== -->\n\n"))
			stitched.WriteString(sr.Result.Text)
			stitched.WriteString("\n")
		}
	}

	finalText := strings.TrimSpace(stitched.String())
	var speedup float64
	if overallDuration.Seconds() > 0 {
		speedup = totalSequentialDur.Seconds() / overallDuration.Seconds()
	}

	term.LogInfo("⚡ OVERCLOCK FINALIZADO EM %.2fs (Estimativa sequencial: %.2fs -> SPEEDUP %.2fx!)",
		overallDuration.Seconds(), totalSequentialDur.Seconds(), speedup)

	return &client.ExecutionResult{
		Text:             finalText,
		Model:            model,
		UsedKeyMasked:    fmt.Sprintf("overclock-parallel (%d workers)", concurrency),
		TTFT:             0,
		TotalDuration:    overallDuration,
		PromptTokens:     totalPromptTokens,
		CandidatesTokens: totalCandTokens,
		TotalTokens:      totalPromptTokens + totalCandTokens,
		TokensPerSecond:  float64(totalCandTokens) / overallDuration.Seconds(),
		Retries:          0,
	}, nil
}
