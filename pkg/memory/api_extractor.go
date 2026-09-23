package memory

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Go regexes
	goFuncRegex = regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?([A-Za-z0-9_]+)\s*(\([^)]*\))\s*(.*?)(\s*\{)?$`)

	// TypeScript / JavaScript regexes
	tsExportFuncRegex  = regexp.MustCompile(`^export\s+(?:async\s+)?function\s+([A-Za-z0-9_]+)\s*(<[^>]+>)?\s*(\([^)]*\))\s*(?::\s*([^{;]+))?`)
	tsExportConstRegex = regexp.MustCompile(`^export\s+const\s+([A-Za-z0-9_]+)\s*:\s*([^=;]+)`)

	// Python regexes
	pyFuncRegex  = regexp.MustCompile(`^\s*def\s+([A-Za-z0-9_]+)\s*(\([^)]*\))\s*(?:->\s*([^:]+))?:`)
	pyClassRegex = regexp.MustCompile(`^\s*class\s+([A-Za-z0-9_]+)(?:\([^)]*\))?:`)

	// Rust regexes
	rsPubFnRegex = regexp.MustCompile(`^\s*pub\s+(?:async\s+)?fn\s+([A-Za-z0-9_]+)\s*(<[^>]+>)?\s*(\([^)]*\))\s*(?:->\s*([^{;]+))?`)
)

// ExtractPublicAPI extracts exported interfaces, structs, types, enums, and signatures
// from a code file, omitting internal function implementations to reduce context window bloat.
func ExtractPublicAPI(path, content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= 45 {
		// Small files don't need pruning
		return strings.TrimSpace(content)
	}

	ext := strings.ToLower(filepath.Ext(path))
	var result string

	switch ext {
	case ".go":
		result = extractGoAPI(lines)
	case ".ts", ".tsx", ".js", ".jsx":
		result = extractTsAPI(lines)
	case ".py":
		result = extractPyAPI(lines)
	case ".rs":
		result = extractRsAPI(lines)
	default:
		// Fallback for languages without dedicated parsers:
		// return declarations / types / exports if detected
		result = extractGenericAPI(lines)
	}

	if len(strings.TrimSpace(result)) < 50 || len(result) >= len(content) {
		return strings.TrimSpace(content)
	}

	return strings.TrimSpace(result)
}

func extractGoAPI(lines []string) string {
	var sb strings.Builder
	inBlock := false
	blockDepth := 0
	inImport := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Preserve package and imports
		if strings.HasPrefix(trimmed, "package ") {
			sb.WriteString(line + "\n\n")
			continue
		}
		if strings.HasPrefix(trimmed, "import (") {
			inImport = true
			sb.WriteString(line + "\n")
			continue
		}
		if inImport {
			sb.WriteString(line + "\n")
			if trimmed == ")" {
				inImport = false
				sb.WriteString("\n")
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			sb.WriteString(line + "\n")
			continue
		}

		// Preserve type declarations (structs, interfaces, aliases)
		if strings.HasPrefix(trimmed, "type ") {
			sb.WriteString(line + "\n")
			if strings.Contains(line, "{") && !strings.Contains(line, "}") {
				inBlock = true
				blockDepth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}

		// Preserve const and var blocks
		if strings.HasPrefix(trimmed, "const (") || strings.HasPrefix(trimmed, "var (") {
			inBlock = true
			blockDepth = 1
			sb.WriteString(line + "\n")
			continue
		}
		if strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "var ") {
			sb.WriteString(line + "\n")
			continue
		}

		// Inside type struct / interface / const block
		if inBlock {
			sb.WriteString(line + "\n")
			blockDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if trimmed == ")" || blockDepth <= 0 {
				inBlock = false
				blockDepth = 0
				sb.WriteString("\n")
			}
			continue
		}

		// Check for functions / methods
		if m := goFuncRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			name := m[1]
			// In Go, uppercase names are exported
			if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
				// Strip body, replace with stub
				sig := trimmed
				if idx := strings.Index(sig, "{"); idx != -1 {
					sig = strings.TrimSpace(sig[:idx])
				}
				sb.WriteString(sig + " { /* ... */ }\n")
			}
		}
	}

	return sb.String()
}

func extractTsAPI(lines []string) string {
	var sb strings.Builder
	inBlock := false
	blockDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Imports
		if strings.HasPrefix(trimmed, "import ") {
			sb.WriteString(line + "\n")
			continue
		}

		// Exports of types, interfaces, enums
		if strings.HasPrefix(trimmed, "export type ") ||
			strings.HasPrefix(trimmed, "export interface ") ||
			strings.HasPrefix(trimmed, "export enum ") {
			sb.WriteString(line + "\n")
			if strings.Contains(line, "{") && !strings.Contains(line, "}") {
				inBlock = true
				blockDepth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}

		// Classes
		if strings.HasPrefix(trimmed, "export class ") || strings.HasPrefix(trimmed, "export abstract class ") {
			sb.WriteString(line + "\n")
			if strings.Contains(line, "{") {
				inBlock = true
				blockDepth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}

		// Inside multi-line interface/type/class
		if inBlock {
			// If inside class, only show method signatures, not function bodies
			if strings.Contains(line, "{") && strings.Contains(line, "}") && strings.Count(line, "{") == strings.Count(line, "}") {
				// Single line method or property
				sb.WriteString(line + "\n")
			} else if strings.Contains(line, "{") {
				blockDepth += strings.Count(line, "{") - strings.Count(line, "}")
				idx := strings.Index(line, "{")
				sb.WriteString(line[:idx] + "{ /* ... */ }\n")
			} else {
				sb.WriteString(line + "\n")
				blockDepth -= strings.Count(line, "}")
				if blockDepth <= 0 {
					inBlock = false
					blockDepth = 0
					sb.WriteString("\n")
				}
			}
			continue
		}

		// Exported functions
		if m := tsExportFuncRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			retType := "void"
			if len(m) > 4 && strings.TrimSpace(m[4]) != "" {
				retType = strings.TrimSpace(m[4])
			}
			sb.WriteString(fmt.Sprintf("export function %s%s%s: %s;\n", m[1], m[2], m[3], retType))
			continue
		}

		// Exported constants with type annotations
		if m := tsExportConstRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			sb.WriteString(fmt.Sprintf("export const %s: %s;\n", m[1], strings.TrimSpace(m[2])))
			continue
		}
	}

	return sb.String()
}

func extractPyAPI(lines []string) string {
	var sb strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") {
			sb.WriteString(line + "\n")
			continue
		}

		if m := pyClassRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			sb.WriteString("\n" + line + "\n")
			continue
		}

		if m := pyFuncRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			ret := ""
			if len(m) > 3 && m[3] != "" {
				ret = " -> " + strings.TrimSpace(m[3])
			}
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			sb.WriteString(fmt.Sprintf("%sdef %s%s%s: ...\n", indent, m[1], m[2], ret))
			continue
		}

		if strings.HasPrefix(trimmed, "__all__") {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

func extractRsAPI(lines []string) string {
	var sb strings.Builder
	inBlock := false
	blockDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "use ") {
			sb.WriteString(line + "\n")
			continue
		}

		if strings.HasPrefix(trimmed, "pub struct ") ||
			strings.HasPrefix(trimmed, "pub enum ") ||
			strings.HasPrefix(trimmed, "pub trait ") ||
			strings.HasPrefix(trimmed, "pub type ") {
			sb.WriteString(line + "\n")
			if strings.Contains(line, "{") && !strings.Contains(line, "}") {
				inBlock = true
				blockDepth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}

		if inBlock {
			sb.WriteString(line + "\n")
			blockDepth += strings.Count(line, "{") - strings.Count(line, "}")
			if blockDepth <= 0 {
				inBlock = false
				blockDepth = 0
				sb.WriteString("\n")
			}
			continue
		}

		if m := rsPubFnRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			ret := ""
			if len(m) > 4 && strings.TrimSpace(m[4]) != "" {
				ret = " -> " + strings.TrimSpace(m[4])
			}
			sb.WriteString(fmt.Sprintf("pub fn %s%s%s%s { /* ... */ }\n", m[1], m[2], m[3], ret))
		}
	}
	return sb.String()
}

func extractGenericAPI(lines []string) string {
	var sb strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") ||
			strings.HasPrefix(trimmed, "type ") ||
			strings.HasPrefix(trimmed, "interface ") ||
			strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "pub ") ||
			strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "struct ") {
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}
