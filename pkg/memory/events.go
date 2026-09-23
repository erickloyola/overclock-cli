package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType classifies a transactional change in the shared memory.
type EventType string

const (
	EventFileRecorded   EventType = "FILE_RECORDED"
	EventFactRecorded   EventType = "FACT_RECORDED"
	EventLessonRecorded EventType = "LESSON_RECORDED"
	EventTaskUpdated    EventType = "TASK_UPDATED"
	EventFileRollback   EventType = "FILE_ROLLBACK"
)

// MemoryEvent represents an immutable log entry in the project's event stream.
type MemoryEvent struct {
	Type      EventType   `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload"`
}

var eventMu sync.Mutex

// AppendEvent appends an event to <outDir>/.overclock/events.jsonl in O(1) time.
func AppendEvent(outDir string, eventType EventType, payload interface{}) error {
	if outDir == "" {
		return nil
	}

	stateDir := filepath.Join(outDir, ".overclock")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("falha ao criar pasta .overclock: %w", err)
	}

	event := MemoryEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		Payload:   payload,
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("falha ao serializar evento: %w", err)
	}

	eventMu.Lock()
	defer eventMu.Unlock()

	eventsPath := filepath.Join(stateDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("falha ao abrir events.jsonl: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("falha ao gravar evento no log: %w", err)
	}

	return nil
}

// LoadEvents reads all events from <outDir>/.overclock/events.jsonl.
func LoadEvents(outDir string) ([]MemoryEvent, error) {
	eventsPath := filepath.Join(outDir, ".overclock", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var events []MemoryEvent
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var ev MemoryEvent
		if err := json.Unmarshal(line, &ev); err == nil {
			events = append(events, ev)
		}
	}
	return events, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
