package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Config encapsulates global CLI settings and runtime configurations.
type Config struct {
	Model        string
	SystemPrompt string
	APIKeys      []string
	Concurrency  int
	Stream       bool
	JSONOutput   bool
	Prune        bool
	PruneLevel   string // "basic", "aggressive", "code", "log"
	CachePrefix  bool
	Timeout      time.Duration
	MaxRetries   int
	Temperature  float64
	Verbose      bool
	OutDir       string
	UseWorktrees bool
}

// DefaultConfig returns optimal production defaults.
func DefaultConfig() *Config {
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.6-flash"
	}
	return &Config{
		Model:       model,
		Concurrency: 4,
		Stream:      true, // Stream by default for instant feedback
		JSONOutput:  false,
		Prune:       false,
		PruneLevel:  "basic",
		CachePrefix: true,
		Timeout:     90 * time.Second,
		MaxRetries:  5,
		Temperature: 0.2,
		Verbose:     false,
	}
}

// LoadAPIKeys extracts keys from explicit files, .env, and environment variables.
// Priority:
// 1. Explicit keysFile passed via flag (if any)
// 2. .env file in current dir or home dir
// 3. Environment variables: GEMINI_API_KEYS, GEMINI_API_KEY_*, GEMINI_API_KEY
func LoadAPIKeys(keysFile string) ([]string, error) {
	var keys []string
	seen := make(map[string]bool)

	addKey := func(k string) {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	// 1. Explicit keys file
	if keysFile != "" {
		if err := readKeysFromFile(keysFile, addKey); err != nil {
			return nil, fmt.Errorf("failed to read keys file %s: %w", keysFile, err)
		}
	}

	// 2. Try loading .env in current directory and home
	_ = readEnvFile(".env", addKey)
	if home, err := os.UserHomeDir(); err == nil {
		_ = readEnvFile(filepath.Join(home, ".overclock.env"), addKey)
		_ = readEnvFile(filepath.Join(home, ".env"), addKey)
	}

	// 3. Check GEMINI_API_KEYS (comma separated)
	if val := os.Getenv("GEMINI_API_KEYS"); val != "" {
		for _, part := range strings.Split(val, ",") {
			addKey(part)
		}
	}

	// 4. Check GEMINI_API_KEY single variable
	if val := os.Getenv("GEMINI_API_KEY"); val != "" {
		addKey(val)
	}

	// 5. Scan numbered env vars (GEMINI_API_KEY_1 to GEMINI_API_KEY_100)
	keyPattern := regexp.MustCompile(`^GEMINI_API_KEY_\d+$`)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 && keyPattern.MatchString(parts[0]) {
			addKey(parts[1])
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("nenhuma chave de API encontrada. Defina GEMINI_API_KEY, GEMINI_API_KEY_1,2... ou crie um arquivo .env")
	}

	return keys, nil
}

func readKeysFromFile(path string, addFn func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow KEY=val or raw key
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			val := strings.Trim(parts[1], `"' `)
			addFn(val)
		} else {
			addFn(line)
		}
	}
	return scanner.Err()
}

func readEnvFile(path string, addFn func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		keyName := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		if keyName == "GEMINI_API_KEY" {
			addFn(val)
		} else if strings.HasPrefix(keyName, "GEMINI_API_KEY_") {
			addFn(val)
		} else if keyName == "GEMINI_API_KEYS" {
			for _, k := range strings.Split(val, ",") {
				addFn(k)
			}
		} else if keyName == "GOOGLE_CLIENT_ID" || keyName == "GOOGLE_CLIENT_SECRET" || keyName == "GEMINI_MODEL" {
			_ = os.Setenv(keyName, val)
		}
	}
	return scanner.Err()
}
