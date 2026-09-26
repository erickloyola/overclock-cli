package hub

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHubLifecycleAndReactiveHandoff(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test-overclock.sock")

	h := NewHub(WithSocketPath(sockPath))
	if err := h.Start(); err != nil {
		t.Fatalf("failed to start hub: %v", err)
	}
	defer func() { _ = h.Close() }()

	// 1. Stage worker configuration (Clean Context)
	workerID := "worker-test-1"
	h.RegisterWorker(WorkerInitPayload{
		WorkerID:  workerID,
		Role:      "Backend Engineer",
		Task:      "Implement login route",
		Contracts: "type LoginRequest struct { Email string }",
		Worktree:  "/path/to/worktree",
		Branch:    "feat/test-login",
	})

	// 2. Simulate worker connecting over Unix domain socket
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to dial hub socket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send register message
	regMsg := Message{
		Type:      MsgTypeRegister,
		WorkerID:  workerID,
		Timestamp: time.Now(),
	}
	regData, _ := json.Marshal(regMsg)
	regData = append(regData, '\n')
	if _, err := conn.Write(regData); err != nil {
		t.Fatalf("failed to send register: %v", err)
	}

	// 3. Worker should receive MsgTypeInit with Clean Context automatically
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("expected message from hub, got none: %v", scanner.Err())
	}

	var initMsg Message
	if err := json.Unmarshal(scanner.Bytes(), &initMsg); err != nil {
		t.Fatalf("failed to parse init msg: %v", err)
	}

	if initMsg.Type != MsgTypeInit {
		t.Errorf("expected msg type 'init', got '%s'", initMsg.Type)
	}

	var payload WorkerInitPayload
	if err := json.Unmarshal(initMsg.Payload, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload.WorkerID != workerID || payload.Task != "Implement login route" {
		t.Errorf("unexpected payload content: %+v", payload)
	}

	// 4. Test Event-Driven Handoff (Zero Polling)
	doneChan := make(chan *HandoffPayload)
	errChan := make(chan error)

	go func() {
		handoff, errWait := h.WaitForHandoff(workerID, 3*time.Second)
		if errWait != nil {
			errChan <- errWait
			return
		}
		doneChan <- handoff
	}()

	// Worker finishes task and sends handoff
	handoffPayload := HandoffPayload{
		Status:    "PASS",
		Artifacts: []string{"internal/auth/login.go", "internal/auth/login_test.go"},
		Notes:     "100% dos testes unitários passando.",
	}
	handoffBytes, _ := json.Marshal(handoffPayload)

	handoffMsg := Message{
		Type:      MsgTypeHandoff,
		WorkerID:  workerID,
		Payload:   handoffBytes,
		Timestamp: time.Now(),
	}
	data, _ := json.Marshal(handoffMsg)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("failed to write handoff msg: %v", err)
	}

	// Assert that Maestro wakes up reactively
	select {
	case result := <-doneChan:
		if result.Status != "PASS" {
			t.Errorf("expected status 'PASS', got '%s'", result.Status)
		}
		if len(result.Artifacts) != 2 {
			t.Errorf("expected 2 artifacts, got %d", len(result.Artifacts))
		}
	case errWait := <-errChan:
		t.Fatalf("wait for handoff failed: %v", errWait)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reactive handoff wakeup")
	}

	// Verify socket cleanup on close
	_ = h.Close()
	if _, errStat := os.Stat(sockPath); !os.IsNotExist(errStat) {
		t.Errorf("expected socket file to be unlinked after Close()")
	}
}
