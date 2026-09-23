package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"overclock/pkg/client"
	"overclock/pkg/memory"
)

// Runner interface for patch generation.
type Runner interface {
	Execute(ctx context.Context, opts client.RequestOptions) (*client.ExecutionResult, error)
	Name() string
}

// SupervisionReport contains quality and consistency diagnostics across all generated files.
type SupervisionReport struct {
	TotalFilesChecked int      `json:"total_files_checked"`
	Passed            bool     `json:"passed"`
	IssuesFound       int      `json:"issues_found"`
	Warnings          []string `json:"warnings"`
	Errors            []string `json:"errors"`
	FixesApplied      []string `json:"fixes_applied"`
}

var (
	tsImportRegex = regexp.MustCompile(`(?:import|from)\s+['"](\.[^'"]+)['"]`)
	todoRegex     = regexp.MustCompile(`(?i)(?://|#)\s*(?:TODO|FIXME|IMPLEMENT LATER|REST OF CODE)`)
)

// ReviewProject checks cross-file consistency, broken imports, missing dependencies, and empty files.
func ReviewProject(bb *memory.Blackboard) *SupervisionReport {
	files := bb.GetAllFiles()
	fileIndex := make(map[string]*memory.FileArtifact)
	for _, f := range files {
		clean := cleanPath(f.Path)
		fileIndex[clean] = f
	}

	rep := &SupervisionReport{
		TotalFilesChecked: len(files),
		Passed:            true,
		Warnings:          make([]string, 0),
		Errors:            make([]string, 0),
		FixesApplied:      make([]string, 0),
	}

	for _, f := range files {
		cleanP := cleanPath(f.Path)
		content := f.Content

		// 1. Check for empty or trivial files
		if len(strings.TrimSpace(content)) < 15 {
			rep.Errors = append(rep.Errors, fmt.Sprintf("[%s] Arquivo vazio ou incompleto (<15 bytes)", cleanP))
			rep.Passed = false
			bb.AddSupervisorNote(memory.SupervisorNote{
				File:        cleanP,
				Severity:    "error",
				Description: "Arquivo gerado vazio ou truncado",
				SuggestedBy: "Supervisor",
			})
			continue
		}

		// 2. Check for lazy placeholders
		if todoRegex.MatchString(content) {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("[%s] Contém marcador de pendência/TODO não implementado", cleanP))
		}

		// 3. Check relative imports in JS/TS
		ext := filepath.Ext(cleanP)
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			matches := tsImportRegex.FindAllStringSubmatch(content, -1)
			dir := filepath.Dir(cleanP)

			for _, m := range matches {
				relTarget := m[1]
				resolved := filepath.Clean(filepath.Join(dir, relTarget))

				// Try with original, or extensions .ts, .tsx, /index.ts, etc.
				candidates := []string{
					resolved,
					resolved + ".ts",
					resolved + ".tsx",
					resolved + ".js",
					resolved + ".jsx",
					filepath.Join(resolved, "index.ts"),
					filepath.Join(resolved, "index.tsx"),
				}

				found := false
				for _, c := range candidates {
					if _, ok := fileIndex[cleanPath(c)]; ok {
						found = true
						break
					}
				}

				if !found {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("[%s] Import relativo '%s' pode não resolver para um arquivo existente", cleanP, relTarget))
				}
			}
		}
	}

	rep.IssuesFound = len(rep.Errors) + len(rep.Warnings)
	if len(rep.Errors) > 0 {
		rep.Passed = false
	}

	return rep
}

// AutoPatchFile requests a surgical patch from the LLM runner to fix a defective file.
func AutoPatchFile(
	ctx context.Context,
	runner Runner,
	bb *memory.Blackboard,
	filePath string,
	issueDescription string,
	model string,
) error {
	fileArt, exists := bb.GetFile(filePath)
	if !exists {
		return fmt.Errorf("arquivo %s não encontrado na memória para patch", filePath)
	}

	systemPrompt := `Você é o Supervisor e Engenheiro de Patch do Overclock.
Sua missão é corrigir um arquivo específico do projeto mantendo total compatibilidade com os contratos globais e com os outros arquivos já gerados.
Responda EXCLUSIVAMENTE com o código final corrigido do arquivo, sem blocos de texto externos.`

	contracts := bb.ContractsSummary()
	prompt := fmt.Sprintf(`[PROBLEMA DETECTADO PELO SUPERVISOR]
Arquivo: %s
Problema: %s

%s

[CÓDIGO ATUAL DO ARQUIVO]
%s

Instrução: Reescreva o arquivo completo %s sanando totalmente o problema detectado.`,
		filePath, issueDescription, contracts, fileArt.Content, filePath)

	res, err := runner.Execute(ctx, client.RequestOptions{
		Model:        model,
		SystemPrompt: systemPrompt,
		Prompt:       prompt,
		Stream:       false,
	})
	if err != nil {
		return err
	}

	cleanCode := stripCodeFences(res.Text)
	bb.RecordFile(filePath, fileArt.Purpose, cleanCode, "Supervisor-Patch")
	return nil
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

func stripCodeFences(raw string) string {
	raw = strings.TrimSpace(raw)
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
