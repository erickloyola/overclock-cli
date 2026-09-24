package engine

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// Matches thinking/monologue tags like <thinking>...</thinking> or [THINKING]...
	thinkingTagRegex = regexp.MustCompile(`(?is)<thinking>.*?</thinking>|\[THINKING\].*?\[/THINKING\]`)
	// Matches raw shell command noise or terminal output snippets
	shellBlockRegex  = regexp.MustCompile("(?s)```(?:bash|sh|zsh|shell|cmd|powershell)\\s*\\n(?:\\$|>)?\\s*(?:cat|ls|cd|mkdir|rm|touch|echo|npm|bun|go run|cargo)\\b.*?\\n```")
)

// SanitizedWorkerDelivery represents a pristine delivery filtered by the Maestro Hub.
type SanitizedWorkerDelivery struct {
	Files          map[string]string `json:"files"`
	Facts          []extractedFact   `json:"facts"`
	CleanSummary   string            `json:"clean_summary"`
	MonologuesSize int               `json:"monologues_pruned_bytes"`
	RawBytes       int               `json:"raw_bytes"`
	CleanBytes     int               `json:"clean_bytes"`
}

// SanitizeWorkerDelivery filters out internal monologues, failed shell commands, and conversational bloat,
// returning only the validated code artifacts and shared facts for downstream workers.
func SanitizeWorkerDelivery(rawText string, expectedFiles []string) *SanitizedWorkerDelivery {
	rawBytes := len(rawText)

	// 1. Strip thinking tags and internal reasoning monologues
	cleaned := thinkingTagRegex.ReplaceAllString(rawText, "")
	monologuesPruned := rawBytes - len(cleaned)

	// 2. Extract facts first before removing other blocks
	facts := extractFactsFromResponse(rawText)

	// 3. Extract code files
	files := extractFilesFromResponse(cleaned, expectedFiles)

	// 4. Calculate clean payload size
	cleanBytes := 0
	for _, content := range files {
		cleanBytes += len(content)
	}

	var generatedList []string
	for p := range files {
		generatedList = append(generatedList, p)
	}

	summary := fmt.Sprintf("%d arquivo(s) extraído(s): [%s] | %d fato(s) registrado(s)",
		len(files), strings.Join(generatedList, ", "), len(facts))

	return &SanitizedWorkerDelivery{
		Files:          files,
		Facts:          facts,
		CleanSummary:   summary,
		MonologuesSize: monologuesPruned,
		RawBytes:       rawBytes,
		CleanBytes:     cleanBytes,
	}
}
