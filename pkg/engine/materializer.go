package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"overclock/pkg/memory"
	"overclock/pkg/supervisor"
	"overclock/pkg/ui"
)

// MaterializeFiles writes a specific list of relative file paths from Blackboard memory directly to disk.
// This is used for incremental saves as each worker completes a task.
func MaterializeFiles(bb *memory.Blackboard, paths []string, outDir string) error {
	if outDir == "" {
		outDir = "."
	}

	for _, p := range paths {
		clean := cleanFilePath(p)
		fileArt, ok := bb.GetFile(clean)
		if !ok {
			fileArt, ok = bb.GetFile(p)
		}
		if !ok || fileArt == nil {
			continue
		}

		fullPath := filepath.Join(outDir, clean)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return fmt.Errorf("falha ao criar pasta para %s: %w", clean, err)
		}

		if err := os.WriteFile(fullPath, []byte(fileArt.Content), 0644); err != nil {
			return fmt.Errorf("falha ao escrever arquivo %s: %w", clean, err)
		}
	}

	return nil
}

// MaterializeBlackboard writes all generated files in the Blackboard directly to disk in outDir.
func MaterializeBlackboard(bb *memory.Blackboard, outDir string) ([]ExtractedFile, error) {
	if outDir == "" {
		outDir = "."
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("falha ao criar pasta de destino: %w", err)
	}

	files := bb.GetAllFiles()
	var written []ExtractedFile

	for _, f := range files {
		clean := cleanFilePath(f.Path)
		fullPath := filepath.Join(outDir, clean)

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, fmt.Errorf("falha ao criar pasta para %s: %w", clean, err)
		}

		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			return nil, fmt.Errorf("falha ao escrever arquivo %s: %w", clean, err)
		}

		written = append(written, ExtractedFile{
			Path:  clean,
			Bytes: len(f.Content),
		})
	}

	// Persist state in .overclock/state.json for auditing/resumption
	_ = bb.SaveState(outDir)

	return written, nil
}

// LifecycleOptions configures post-creation operations.
type LifecycleOptions struct {
	InstallDeps bool
	VerifyBuild bool
	InitGit     bool
}

// ExecuteLifecycleHooks executes requested post-creation system operations safely inside outDir.
func ExecuteLifecycleHooks(
	ctx context.Context,
	bb *memory.Blackboard,
	outDir string,
	opts LifecycleOptions,
	runner Runner,
	model string,
	term *ui.Terminal,
) error {
	manifest := bb.GetManifest()

	// 1. Git Initialization
	if opts.InitGit {
		term.LogInfo("🌱 Inicializando repositório Git em %s...", outDir)
		_, _ = runLocalCmd(ctx, outDir, "git", "init")
		_, _ = runLocalCmd(ctx, outDir, "git", "add", ".")
		_, _ = runLocalCmd(ctx, outDir, "git", "commit", "-m", "Initial commit by Overclock CLI")
	}

	// 2. Dependency Installation
	if opts.InstallDeps {
		pm := strings.ToLower(manifest.PackageManager)
		var cmdName string
		var cmdArgs []string

		switch {
		case pm == "bun" || hasFile(outDir, "bun.lockb"):
			cmdName = "bun"
			cmdArgs = []string{"install"}
		case pm == "pnpm" || hasFile(outDir, "pnpm-lock.yaml"):
			cmdName = "pnpm"
			cmdArgs = []string{"install"}
		case pm == "yarn" || hasFile(outDir, "yarn.lock"):
			cmdName = "yarn"
			cmdArgs = []string{"install"}
		case pm == "npm" || hasFile(outDir, "package.json"):
			cmdName = "npm"
			cmdArgs = []string{"install"}
		case pm == "go" || hasFile(outDir, "go.mod"):
			cmdName = "go"
			cmdArgs = []string{"mod", "tidy"}
		case pm == "cargo" || hasFile(outDir, "Cargo.toml"):
			cmdName = "cargo"
			cmdArgs = []string{"check"}
		case pm == "pip" || hasFile(outDir, "requirements.txt"):
			cmdName = "pip"
			cmdArgs = []string{"install", "-r", "requirements.txt"}
		case pm == "poetry" || hasFile(outDir, "poetry.lock"):
			cmdName = "poetry"
			cmdArgs = []string{"install"}
		case pm == "make" || hasFile(outDir, "Makefile"):
			cmdName = "make"
		default:
			term.LogWarn("Nenhum gerenciador de dependências aplicável identificado para auto-instalação.")
		}

		if cmdName != "" {
			term.LogInfo("📦 Instalando dependências com '%s %s'...", cmdName, strings.Join(cmdArgs, " "))
			t0 := time.Now()
			out, err := runLocalCmd(ctx, outDir, cmdName, cmdArgs...)
			if err != nil {
				term.LogError("❌ Falha na instalação de dependências: %v\nOutput: %s", err, out)
			} else {
				term.LogInfo("✅ Dependências instaladas com sucesso em %.2fs", time.Since(t0).Seconds())
			}
		}
	}

	// 3. Build Verification and Self-Correction Loop
	if opts.VerifyBuild {
		buildCommand := manifest.BuildCommand
		if buildCommand == "" {
			if hasFile(outDir, "go.mod") {
				buildCommand = "go build ./..."
			} else if hasFile(outDir, "Cargo.toml") {
				buildCommand = "cargo check"
			} else if hasFile(outDir, "Makefile") {
				buildCommand = "make"
			} else if hasFile(outDir, "package.json") {
				buildCommand = "npm run build"
			}
		}

		if buildCommand != "" {
			term.LogInfo("🔍 Verificando integridade de build do projeto com '%s'...", buildCommand)
			parts := strings.Fields(buildCommand)
			if len(parts) > 0 {
				out, err := runLocalCmd(ctx, outDir, parts[0], parts[1:]...)
				if err == nil {
					term.LogInfo("✅ Build verificado e aprovado com sucesso pelo compilador real do sistema!")
				} else {
					term.LogWarn("⚠️ O compilador acusou divergência no build. Acionando ciclo de auto-correção...")
					term.LogInfo("Erro do compilador:\n%s", truncate(out, 600))

					// Try to locate error file from output
					errFile := findOffendingFile(out, bb.GetAllFiles())
					bb.RecordLesson("compiler", truncate(out, 300), "Ajuste os imports e assinaturas reportados pelo compilador", errFile)

					if errFile != "" {
						term.LogInfo("🔧 Supervisor aplicando patch cirúrgico em %s...", errFile)
						patchErr := supervisor.AutoPatchFile(ctx, runner, bb, errFile, out, model)
						if patchErr == nil {
							// Rewrite patched file
							if fArt, ok := bb.GetFile(errFile); ok {
								_ = os.WriteFile(filepath.Join(outDir, errFile), []byte(fArt.Content), 0644)
								term.LogInfo("🔄 Re-testando build após patch...")
								outRetry, errRetry := runLocalCmd(ctx, outDir, parts[0], parts[1:]...)
								if errRetry == nil {
									term.LogInfo("✅ Build aprovado com sucesso após auto-correção!")
									bb.RecordFact(memory.FactCategoryRuntime, "build_status", "verified", "Compiler")
									bb.RecordLesson("compiler-fix", "Divergência de compilação resolvida com sucesso", fmt.Sprintf("Arquivo %s aprovado no rebuild", errFile), errFile)
								} else {
									term.LogWarn("⚠️ Build ainda reporta avisos após auto-correção: %s", truncate(outRetry, 300))
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}

func hasFile(dir, filename string) bool {
	_, err := os.Stat(filepath.Join(dir, filename))
	return err == nil
}

func runLocalCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	return strings.TrimSpace(outBuf.String()), err
}

func findOffendingFile(compilerOutput string, files []*memory.FileArtifact) string {
	for _, f := range files {
		base := filepath.Base(f.Path)
		if strings.Contains(compilerOutput, f.Path) || strings.Contains(compilerOutput, base) {
			return f.Path
		}
	}
	return ""
}
