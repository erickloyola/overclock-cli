package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractedFile represents a file extracted and written to disk.
type ExtractedFile struct {
	Path  string
	Bytes int
}

var (
	// Matches: // types/benchmark.ts or // src/components/Metric.tsx
	inlinePathRegex = regexp.MustCompile(`^(?://|#)\s*(?:file:\s*|path:\s*)?([a-zA-Z0-9_.\-\/]+\.[a-zA-Z0-9]+)`)
	// Matches: ### 1. `types.ts` or ### 2. MetricCard.tsx or (types/benchmark.ts)
	headerPathRegex = regexp.MustCompile(`(?:` + "`" + `|\()([a-zA-Z0-9_.\-\/]+\.[a-zA-Z0-9]+)(?:` + "`" + `|\))`)
	headerDirectRegex = regexp.MustCompile(`###\s+(?:\d+\.\s+)?([a-zA-Z0-9_.\-\/]+\.[a-zA-Z0-9]+)`)
)

// MaterializeProject scans markdown text for code blocks, determines their relative filepaths,
// and writes them directly to the specified target directory.
func MaterializeProject(markdownText string, targetDir string) ([]ExtractedFile, error) {
	if targetDir == "" {
		targetDir = "."
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("falha ao criar diretório do projeto: %w", err)
	}

	lines := strings.Split(markdownText, "\n")
	var extracted []ExtractedFile
	var currentBlock []string
	inBlock := false
	blockLang := ""
	lastHeader := ""
	fileIndex := 1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "###") || strings.HasPrefix(trimmed, "####") {
			lastHeader = trimmed
		}

		if strings.HasPrefix(trimmed, "```") && !inBlock {
			inBlock = true
			blockLang = strings.TrimPrefix(trimmed, "```")
			blockLang = strings.TrimSpace(blockLang)
			currentBlock = nil
			continue
		} else if strings.HasPrefix(trimmed, "```") && inBlock {
			inBlock = false

			// Skip mermaid diagrams or shell output logs unless shell scripts
			if blockLang == "mermaid" || blockLang == "text" || blockLang == "bash" && strings.Contains(strings.ToLower(lastHeader), "terminal") {
				continue
			}

			if len(currentBlock) == 0 {
				continue
			}

			content := strings.Join(currentBlock, "\n")
			targetRelPath := detectFilePath(lastHeader, currentBlock, blockLang, fileIndex)
			if targetRelPath == "" {
				continue
			}

			fullPath := filepath.Join(targetDir, targetRelPath)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return nil, fmt.Errorf("falha ao criar pasta para %s: %w", targetRelPath, err)
			}

			if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
				return nil, fmt.Errorf("falha ao escrever arquivo %s: %w", targetRelPath, err)
			}

			extracted = append(extracted, ExtractedFile{
				Path:  targetRelPath,
				Bytes: len(content),
			})
			fileIndex++
			continue
		}

		if inBlock {
			currentBlock = append(currentBlock, line)
		}
	}

	return extracted, nil
}

func detectFilePath(header string, blockLines []string, lang string, index int) string {
	// 1. Check first line of block for comment path (// src/components/X.tsx)
	if len(blockLines) > 0 {
		firstLine := strings.TrimSpace(blockLines[0])
		if match := inlinePathRegex.FindStringSubmatch(firstLine); len(match) > 1 {
			p := cleanPath(match[1])
			if isValidFilePath(p) {
				return p
			}
		}
	}

	// 2. Check header for parenthesized or backticked path (`types.ts` or (types/benchmark.ts))
	if header != "" {
		if match := headerPathRegex.FindStringSubmatch(header); len(match) > 1 {
			p := cleanPath(match[1])
			if isValidFilePath(p) {
				return p
			}
		}
		if match := headerDirectRegex.FindStringSubmatch(header); len(match) > 1 {
			p := cleanPath(match[1])
			if isValidFilePath(p) {
				return p
			}
		}
	}

	// 3. Fallback based on language and index
	ext := ""
	switch lang {
	case "go", "golang":
		ext = ".go"
	case "python", "py":
		ext = ".py"
	case "rust", "rs":
		ext = ".rs"
	case "c":
		ext = ".c"
	case "cpp", "c++":
		ext = ".cpp"
	case "typescript", "ts":
		ext = ".ts"
	case "tsx":
		ext = ".tsx"
	case "javascript", "js":
		ext = ".js"
	case "jsx":
		ext = ".jsx"
	case "json":
		ext = ".json"
	case "css":
		ext = ".css"
	case "html":
		if index == 1 {
			return "index.html"
		}
		return fmt.Sprintf("page_%d.html", index)
	default:
		return ""
	}

	return fmt.Sprintf("src/generated_module_%d%s", index, ext)
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

func isValidFilePath(p string) bool {
	ext := filepath.Ext(p)
	if ext == "" {
		return false
	}
	validExts := map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".json": true, ".css": true, ".html": true, ".go": true,
		".py": true, ".md": true, ".yaml": true, ".yml": true,
	}
	return validExts[ext]
}
