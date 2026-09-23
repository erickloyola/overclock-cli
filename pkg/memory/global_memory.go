package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GlobalMemory holds persistent knowledge and developer conventions across all projects.
type GlobalMemory struct {
	mu          sync.RWMutex
	Facts       map[string]*SharedFact `json:"facts"`
	Conventions []string               `json:"conventions"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// GetGlobalMemoryPath returns the standard path for global memory storage.
func GetGlobalMemoryPath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".", ".overclock-global.json")
		}
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "overclock", "global_memory.json")
}

// LoadGlobalMemory loads the global memory from disk, or initializes a new one.
func LoadGlobalMemory() (*GlobalMemory, error) {
	path := GetGlobalMemoryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &GlobalMemory{
				Facts:       make(map[string]*SharedFact),
				Conventions: make([]string, 0),
				UpdatedAt:   time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("falha ao ler memória global: %w", err)
	}

	var gm GlobalMemory
	if err := json.Unmarshal(data, &gm); err != nil {
		return nil, fmt.Errorf("falha ao decodificar memória global: %w", err)
	}

	if gm.Facts == nil {
		gm.Facts = make(map[string]*SharedFact)
	}
	if gm.Conventions == nil {
		gm.Conventions = make([]string, 0)
	}

	return &gm, nil
}

// Save writes the global memory to disk atomically.
func (g *GlobalMemory) Save() error {
	g.mu.RLock()
	g.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(g, "", "  ")
	g.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("falha ao serializar memória global: %w", err)
	}

	path := GetGlobalMemoryPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("falha ao criar pasta para memória global: %w", err)
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf("global-%d.tmp", time.Now().UnixNano()))
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("falha ao gravar arquivo temporário da memória global: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("falha ao renomear memória global: %w", err)
	}

	return nil
}

// SetFact registers or updates a global shared fact.
func (g *GlobalMemory) SetFact(category FactCategory, key, value, source string) *SharedFact {
	g.mu.Lock()
	defer g.mu.Unlock()

	normKey := strings.ToLower(strings.TrimSpace(key))
	fact := &SharedFact{
		ID:        fmt.Sprintf("global_fact_%d", len(g.Facts)+1),
		Category:  category,
		Key:       normKey,
		Value:     strings.TrimSpace(value),
		Source:    source,
		CreatedAt: time.Now(),
	}
	g.Facts[normKey] = fact
	return fact
}

// GetFact retrieves a global shared fact by key.
func (g *GlobalMemory) GetFact(key string) (*SharedFact, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	fact, ok := g.Facts[strings.ToLower(strings.TrimSpace(key))]
	return fact, ok
}

// DeleteFact removes a fact from global memory.
func (g *GlobalMemory) DeleteFact(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	normKey := strings.ToLower(strings.TrimSpace(key))
	if _, exists := g.Facts[normKey]; exists {
		delete(g.Facts, normKey)
		return true
	}
	return false
}

// GetFacts returns a copy of all global facts.
func (g *GlobalMemory) GetFacts() []*SharedFact {
	g.mu.RLock()
	defer g.mu.RUnlock()
	list := make([]*SharedFact, 0, len(g.Facts))
	for _, f := range g.Facts {
		list = append(list, f)
	}
	return list
}

// AddConvention appends a global architectural convention.
func (g *GlobalMemory) AddConvention(conv string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	conv = strings.TrimSpace(conv)
	for _, c := range g.Conventions {
		if c == conv {
			return
		}
	}
	g.Conventions = append(g.Conventions, conv)
}

// MergeGlobalMemory injects relevant global facts and conventions into the project blackboard.
func (b *Blackboard) MergeGlobalMemory(g *GlobalMemory) {
	if g == nil {
		return
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	for k, fact := range g.Facts {
		if _, exists := b.Facts[k]; !exists {
			b.Facts[k] = &SharedFact{
				ID:        fact.ID,
				Category:  fact.Category,
				Key:       fact.Key,
				Value:     fact.Value,
				Source:    "GlobalMemory",
				CreatedAt: fact.CreatedAt,
			}
		}
	}

	for _, conv := range g.Conventions {
		found := false
		for _, bc := range b.Manifest.Conventions {
			if bc == conv {
				found = true
				break
			}
		}
		if !found {
			b.Manifest.Conventions = append(b.Manifest.Conventions, conv)
		}
	}
}
