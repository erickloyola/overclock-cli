package hub

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Message types exchanged over the Unix domain socket.
const (
	MsgTypeRegister = "register"
	MsgTypeInit     = "init"
	MsgTypeWrite    = "write"
	MsgTypeHandoff  = "handoff"
	MsgTypeLog      = "log"
	MsgTypeDismiss  = "dismiss"
	MsgTypePing     = "ping"
	MsgTypePong     = "pong"
)

// DefaultSocketPath defines the default Unix domain socket path.
const DefaultSocketPath = "/tmp/overclock.sock"

// WorkerInitPayload contains the sanitized Clean Context and specifications for a worker.
type WorkerInitPayload struct {
	WorkerID  string `json:"worker_id"`
	Role      string `json:"role"`
	Task      string `json:"task"`
	Contracts string `json:"contracts,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Preset    string `json:"preset,omitempty"`
}

// HandoffPayload represents the structured completion report from a worker.
type HandoffPayload struct {
	Status    string   `json:"status"` // "PASS" or "FAIL"
	Artifacts []string `json:"artifacts"`
	Notes     string   `json:"notes"`
}

// Message is the JSON envelope sent across the IPC socket.
type Message struct {
	Type      string          `json:"type"`
	WorkerID  string          `json:"id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

type workerSession struct {
	id          string
	initPayload *WorkerInitPayload
	conn        net.Conn
	writer      *bufio.Writer
	handoffChan chan *HandoffPayload
	handoff     *HandoffPayload
	connectedAt time.Time
}

// Hub manages Unix domain socket IPC between the Maestro and worker terminal panes.
type Hub struct {
	socketPath string
	listener   net.Listener
	mu         sync.RWMutex
	workers    map[string]*workerSession
	closed     bool
	onLog      func(workerID string, text string)
}

// HubOption configures the Hub instance.
type HubOption func(*Hub)

// WithSocketPath overrides the default socket path.
func WithSocketPath(path string) HubOption {
	return func(h *Hub) {
		h.socketPath = path
	}
}

// WithLogCallback configures real-time log ingestion.
func WithLogCallback(cb func(workerID string, text string)) HubOption {
	return func(h *Hub) {
		h.onLog = cb
	}
}

// NewHub initializes an IPC Hub for multi-pane orchestration.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		socketPath: DefaultSocketPath,
		workers:    make(map[string]*workerSession),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// SocketPath returns the configured socket path.
func (h *Hub) SocketPath() string {
	return h.socketPath
}

// Start binds and begins listening on the Unix Domain Socket.
func (h *Hub) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Ensure directory exists
	sockDir := filepath.Dir(h.socketPath)
	if err := os.MkdirAll(sockDir, 0755); err != nil {
		return fmt.Errorf("falha ao criar pasta para socket %s: %w", sockDir, err)
	}

	// Remove stale socket if not active, or allocate PID-based socket if active
	if _, err := os.Stat(h.socketPath); err == nil {
		if c, errConn := net.DialTimeout("unix", h.socketPath, 100*time.Millisecond); errConn == nil {
			_ = c.Close()
			if h.socketPath == DefaultSocketPath {
				h.socketPath = fmt.Sprintf("/tmp/overclock-%d.sock", os.Getpid())
			} else {
				return fmt.Errorf("socket %s já está em uso por outro processo", h.socketPath)
			}
		} else {
			_ = os.Remove(h.socketPath)
		}
	}

	listener, err := net.Listen("unix", h.socketPath)
	if err != nil {
		return fmt.Errorf("falha ao abrir socket unix em %s: %w", h.socketPath, err)
	}
	h.listener = listener

	go h.acceptLoop()
	return nil
}

func (h *Hub) acceptLoop() {
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			h.mu.RLock()
			isClosed := h.closed
			h.mu.RUnlock()
			if isClosed {
				return
			}
			continue
		}

		go h.handleConnection(conn)
	}
}

func (h *Hub) handleConnection(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	var registeredID string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case MsgTypeRegister:
			registeredID = msg.WorkerID
			h.bindWorkerConnection(msg.WorkerID, conn)

		case MsgTypeHandoff:
			var handoff HandoffPayload
			if err := json.Unmarshal(msg.Payload, &handoff); err == nil {
				h.SubmitHandoff(msg.WorkerID, &handoff)
			}

		case MsgTypeLog:
			var logText string
			if err := json.Unmarshal(msg.Payload, &logText); err == nil {
				if h.onLog != nil {
					h.onLog(msg.WorkerID, logText)
				}
			}

		case MsgTypePing:
			_ = h.sendRaw(conn, Message{
				Type:      MsgTypePong,
				WorkerID:  msg.WorkerID,
				Timestamp: time.Now(),
			})
		}
	}

	if registeredID != "" {
		h.mu.Lock()
		if session, ok := h.workers[registeredID]; ok && session.conn == conn {
			session.conn = nil
			session.writer = nil
		}
		h.mu.Unlock()
	}
	_ = conn.Close()
}

// RegisterWorker stages worker configuration (Clean Context) before spawning.
func (h *Hub) RegisterWorker(initPayload WorkerInitPayload) {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, exists := h.workers[initPayload.WorkerID]
	if !exists {
		session = &workerSession{
			id:          initPayload.WorkerID,
			handoffChan: make(chan *HandoffPayload, 1),
		}
		h.workers[initPayload.WorkerID] = session
	}
	session.initPayload = &initPayload
}

func (h *Hub) bindWorkerConnection(workerID string, conn net.Conn) {
	h.mu.Lock()
	session, exists := h.workers[workerID]
	if !exists {
		session = &workerSession{
			id:          workerID,
			handoffChan: make(chan *HandoffPayload, 1),
		}
		h.workers[workerID] = session
	}
	session.conn = conn
	session.writer = bufio.NewWriter(conn)
	session.connectedAt = time.Now()
	initPayload := session.initPayload
	h.mu.Unlock()

	// Automatically inject Clean Context upon connection
	if initPayload != nil {
		payloadBytes, _ := json.Marshal(initPayload)
		_ = h.sendRaw(conn, Message{
			Type:      MsgTypeInit,
			WorkerID:  workerID,
			Payload:   payloadBytes,
			Timestamp: time.Now(),
		})
	}
}

func (h *Hub) sendRaw(conn net.Conn, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

// SendToWorker sends a message to an active connected worker.
func (h *Hub) SendToWorker(workerID string, msgType string, payload interface{}) error {
	h.mu.RLock()
	session, ok := h.workers[workerID]
	if !ok || session.conn == nil {
		h.mu.RUnlock()
		return fmt.Errorf("worker '%s' não está conectado", workerID)
	}
	conn := session.conn
	h.mu.RUnlock()

	var payloadBytes []byte
	if payload != nil {
		var err error
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	return h.sendRaw(conn, Message{
		Type:      msgType,
		WorkerID:  workerID,
		Payload:   payloadBytes,
		Timestamp: time.Now(),
	})
}

// SubmitHandoff registers a worker's completion report and notifies listeners reactively (Zero Polling).
func (h *Hub) SubmitHandoff(workerID string, handoff *HandoffPayload) {
	h.mu.Lock()
	session, ok := h.workers[workerID]
	if !ok {
		session = &workerSession{
			id:          workerID,
			handoffChan: make(chan *HandoffPayload, 1),
		}
		h.workers[workerID] = session
	}
	session.handoff = handoff

	// Non-blocking send to channel
	select {
	case session.handoffChan <- handoff:
	default:
	}
	h.mu.Unlock()
}

// WaitForHandoff blocks until the specified worker reports completion or timeout occurs (Event-Driven Reactive Wakeup).
func (h *Hub) WaitForHandoff(workerID string, timeout time.Duration) (*HandoffPayload, error) {
	h.mu.RLock()
	session, ok := h.workers[workerID]
	if !ok {
		h.mu.RUnlock()
		return nil, fmt.Errorf("worker '%s' não encontrado", workerID)
	}
	// If already completed, return immediately
	if session.handoff != nil {
		h.mu.RUnlock()
		return session.handoff, nil
	}
	ch := session.handoffChan
	h.mu.RUnlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case handoff := <-ch:
		return handoff, nil
	case <-timer.C:
		return nil, errors.New("timeout aguardando handoff do worker")
	}
}

// GetHandoff retrieves the handoff result if already submitted.
func (h *Hub) GetHandoff(workerID string) (*HandoffPayload, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	session, ok := h.workers[workerID]
	if !ok || session.handoff == nil {
		return nil, false
	}
	return session.handoff, true
}

// ListWorkers returns IDs of all registered workers.
func (h *Hub) ListWorkers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := make([]string, 0, len(h.workers))
	for id := range h.workers {
		list = append(list, id)
	}
	return list
}

// UnregisterWorker removes a worker session.
func (h *Hub) UnregisterWorker(workerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session, ok := h.workers[workerID]; ok {
		if session.conn != nil {
			_ = session.conn.Close()
		}
		delete(h.workers, workerID)
	}
}

// Close terminates the IPC Hub and unlinks the Unix socket.
func (h *Hub) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true

	for _, session := range h.workers {
		if session.conn != nil {
			_ = session.conn.Close()
		}
	}
	h.workers = make(map[string]*workerSession)

	var err error
	if h.listener != nil {
		err = h.listener.Close()
	}
	_ = os.Remove(h.socketPath)
	return err
}
