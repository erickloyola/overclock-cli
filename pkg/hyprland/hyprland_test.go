package hyprland

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildTerminalCommand(t *testing.T) {
	tests := []struct {
		name        string
		opts        WindowOptions
		expectedBin string
		mustContain []string
	}{
		{
			name: "Kitty default options",
			opts: WindowOptions{
				Class:     "overclock-worker",
				Title:     "⚡ Overclock: worker-backend",
				Directory: "/home/erickloyola/app/.worktrees/worker-backend",
				Command:   "overclock worker --id worker-backend",
				Terminal:  TerminalKitty,
			},
			expectedBin: "kitty",
			mustContain: []string{"--class", "overclock-worker", "--title", "⚡ Overclock: worker-backend", "--directory", "/home/erickloyola/app/.worktrees/worker-backend", "sh", "-c"},
		},
		{
			name: "Foot options",
			opts: WindowOptions{
				Class:    "overclock-worker",
				Title:    "Worker Foot",
				Command:  "echo hello",
				Terminal: TerminalFoot,
			},
			expectedBin: "foot",
			mustContain: []string{"--app-id", "overclock-worker", "--title", "Worker Foot"},
		},
		{
			name: "Alacritty options",
			opts: WindowOptions{
				Class:    "overclock-worker",
				Title:    "Worker Alacritty",
				Command:  "echo hello",
				Terminal: TerminalAlacritty,
			},
			expectedBin: "alacritty",
			mustContain: []string{"--class", "overclock-worker", "-e", "sh", "-c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, args, err := BuildTerminalCommand(tt.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bin != tt.expectedBin {
				t.Errorf("expected bin %s, got %s", tt.expectedBin, bin)
			}
			joined := strings.Join(args, " ")
			for _, exp := range tt.mustContain {
				if !strings.Contains(joined, exp) {
					t.Errorf("expected args to contain '%s', got '%s'", exp, joined)
				}
			}
		})
	}
}

func TestControllerMockExecution(t *testing.T) {
	c := &Controller{
		hyprctlPath: "/usr/bin/hyprctl",
		execCmd: func(name string, args ...string) ([]byte, error) {
			cmdStr := name + " " + strings.Join(args, " ")
			if strings.Contains(cmdStr, "version") {
				return []byte("Hyprland 0.40.0"), nil
			}
			if strings.Contains(cmdStr, "dispatch exec") {
				return []byte("ok"), nil
			}
			if strings.Contains(cmdStr, "dispatch closewindow") {
				return []byte("ok"), nil
			}
			if strings.Contains(cmdStr, "-j clients") {
				return []byte(`[{"class":"overclock-worker","title":"⚡ Overclock: worker-1","pid":1234}]`), nil
			}
			return nil, errors.New("unknown command")
		},
	}

	// Fake instance signature for mock test
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "mock-signature")

	if !c.IsAvailable() {
		t.Errorf("expected controller to be available")
	}

	err := c.SpawnWindow(WindowOptions{
		Class:   "overclock-worker",
		Title:   "⚡ Overclock: worker-1",
		Command: "overclock worker --id worker-1",
	})
	if err != nil {
		t.Fatalf("unexpected spawn error: %v", err)
	}

	err = c.CloseWindowByTitle("⚡ Overclock: worker-1")
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	clients, err := c.ListClients()
	if err != nil {
		t.Fatalf("unexpected list clients error: %v", err)
	}
	if len(clients) != 1 || clients[0].Class != "overclock-worker" {
		t.Errorf("unexpected clients result: %+v", clients)
	}
}
