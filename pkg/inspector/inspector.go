package inspector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"overclock/pkg/pruner"
)

// ToolInfo contains version and availability of an installed CLI tool.
type ToolInfo struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

// SystemEnvironment describes the host machine's development environment.
type SystemEnvironment struct {
	OS          string              `json:"os"`
	Arch        string              `json:"arch"`
	CPUCores    int                 `json:"cpu_cores"`
	Tools       map[string]ToolInfo `json:"tools"`
	Recommended map[string]string   `json:"recommended"`
}

// InspectSystem probes the local system for available runtimes and dev tools.
func InspectSystem(ctx context.Context) *SystemEnvironment {
	env := &SystemEnvironment{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CPUCores:    runtime.NumCPU(),
		Tools:       make(map[string]ToolInfo),
		Recommended: make(map[string]string),
	}

	toolNames := []string{
		"bun", "pnpm", "yarn", "npm", "node",
		"go", "cargo", "rustc", "python3", "python",
		"uv", "docker", "git", "make",
	}

	for _, name := range toolNames {
		info := detectTool(ctx, name)
		env.Tools[name] = info
	}

	// Recommendations by ecosystem
	if env.Tools["bun"].Available {
		env.Recommended["javascript_typescript"] = "bun"
	} else if env.Tools["pnpm"].Available {
		env.Recommended["javascript_typescript"] = "node + pnpm"
	} else if env.Tools["npm"].Available {
		env.Recommended["javascript_typescript"] = "node + npm"
	}

	if env.Tools["go"].Available {
		env.Recommended["go"] = fmt.Sprintf("go (%s)", env.Tools["go"].Version)
	}
	if env.Tools["cargo"].Available {
		env.Recommended["rust"] = fmt.Sprintf("cargo (%s)", env.Tools["cargo"].Version)
	}
	if env.Tools["python3"].Available {
		env.Recommended["python"] = fmt.Sprintf("python3 (%s)", env.Tools["python3"].Version)
	} else if env.Tools["python"].Available {
		env.Recommended["python"] = fmt.Sprintf("python (%s)", env.Tools["python"].Version)
	}
	if env.Tools["docker"].Available {
		env.Recommended["containers"] = "docker"
	}

	return env
}

func detectTool(ctx context.Context, name string) ToolInfo {
	path, err := exec.LookPath(name)
	if err != nil {
		return ToolInfo{Name: name, Available: false}
	}

	tCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(tCtx, path, "--version")
	out, err := cmd.Output()
	version := ""
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			version = strings.TrimSpace(lines[0])
		}
	}

	return ToolInfo{
		Name:      name,
		Available: true,
		Path:      path,
		Version:   version,
	}
}

// SummaryString formats a concise description of the host environment for the AI architect.
func (env *SystemEnvironment) SummaryString() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Host OS: %s/%s (%d CPU cores)\n", env.OS, env.Arch, env.CPUCores))
	sb.WriteString("Disponíveis no sistema:\n")

	for name, tool := range env.Tools {
		if tool.Available {
			if tool.Version != "" {
				sb.WriteString(fmt.Sprintf("• %s (%s)\n", name, tool.Version))
			} else {
				sb.WriteString(fmt.Sprintf("• %s (instalado)\n", name))
			}
		}
	}

	if len(env.Recommended) > 0 {
		sb.WriteString("Ferramentas disponíveis no sistema hospedeiro:\n")
		for k, v := range env.Recommended {
			sb.WriteString(fmt.Sprintf("• %s: %s\n", k, v))
		}
		sb.WriteString("DIRETRIZ: Selecione a linguagem, runtime e ferramentas de forma autônoma e estritamente adequada ao domínio do projeto solicitado, sem favorecer nenhuma stack pré-concebida.\n")
	}

	return sb.String()
}

// LoadContextFiles loads and prunes local files/schemas passed via flags like --context or --from.
func LoadContextFiles(paths []string, pruneOpts pruner.Options) (string, error) {
	if len(paths) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("arquivo de contexto não encontrado: %s", p)
		}

		if info.IsDir() {
			// Read top files in dir
			entries, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				subPath := filepath.Join(p, entry.Name())
				data, rErr := os.ReadFile(subPath)
				if rErr != nil {
					continue
				}
				pruned, _ := pruner.Prune(string(data), pruneOpts)
				sb.WriteString(fmt.Sprintf("\n[ARQUIVO DE CONTEXTO: %s]\n%s\n", entry.Name(), pruned))
			}
		} else {
			data, err := os.ReadFile(p)
			if err != nil {
				return "", fmt.Errorf("erro ao ler arquivo %s: %w", p, err)
			}
			pruned, _ := pruner.Prune(string(data), pruneOpts)
			sb.WriteString(fmt.Sprintf("\n[ARQUIVO DE CONTEXTO: %s]\n%s\n", filepath.Base(p), pruned))
		}
	}

	return sb.String(), nil
}
