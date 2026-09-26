package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"overclock/pkg/hub"
)

func handleWorker(args []string) {
	var (
		workerID string
		sockPath string = hub.DefaultSocketPath
		preset   string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--id" || arg == "-i") && i+1 < len(args) {
			workerID = args[i+1]
			i++
		} else if (arg == "--sock" || arg == "-s") && i+1 < len(args) {
			sockPath = args[i+1]
			i++
		} else if (arg == "--preset" || arg == "-p") && i+1 < len(args) {
			preset = args[i+1]
			i++
		} else if !strings.HasPrefix(arg, "-") && workerID == "" {
			workerID = arg
		}
	}

	if workerID == "" {
		fmt.Fprintf(os.Stderr, "Erro: --id é obrigatório para o subcomando worker\n")
		fmt.Println("Uso: overclock worker --id <NOME> [--sock <SOCKET>] [--preset agy|bash]")
		os.Exit(1)
	}

	// 1. Tenta conectar ao Hub IPC com retries
	var conn net.Conn
	var err error
	for attempt := 0; attempt < 25; attempt++ {
		conn, err = net.Dial("unix", sockPath)
		if err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Falha ao conectar ao Hub IPC em %s: %v\n", sockPath, err)
		fmt.Println("\033[2mPressione ENTER para fechar a janela...\033[0m")
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	// 2. Registro do worker
	regMsg := hub.Message{
		Type:      hub.MsgTypeRegister,
		WorkerID:  workerID,
		Timestamp: time.Now(),
	}
	regData, _ := json.Marshal(regMsg)
	regData = append(regData, '\n')
	if _, err := conn.Write(regData); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Erro ao enviar registro para o Hub: %v\n", err)
		os.Exit(1)
	}

	// 3. Aguarda o payload de inicialização com Clean Context
	scanner := bufio.NewScanner(conn)
	var initPayload hub.WorkerInitPayload

	for scanner.Scan() {
		var msg hub.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type == hub.MsgTypeInit {
			_ = json.Unmarshal(msg.Payload, &initPayload)
			break
		}
	}

	if preset == "" && initPayload.Preset != "" {
		preset = initPayload.Preset
	}
	if preset == "" {
		preset = "agy"
	}

	cwd, _ := os.Getwd()

	// 4. Renderiza o cabeçalho visual do Cockpit Worker
	fmt.Printf("\033[1;36m⚡ [OVERCLOCK WORKER PANE: %s]\033[0m\n", workerID)
	fmt.Println(strings.Repeat("─", 70))
	if initPayload.Role != "" {
		fmt.Printf("\033[1;33m  Papel:\033[0m      %s\n", initPayload.Role)
	}
	fmt.Printf("\033[1;33m  Diretório:\033[0m  %s\n", cwd)
	if initPayload.Branch != "" {
		fmt.Printf("\033[1;33m  Branch:\033[0m     %s\n", initPayload.Branch)
	}
	if initPayload.Task != "" {
		fmt.Printf("\033[1;32m\n🎯 TAREFA ATRIBUÍDA (Clean Context):\033[0m\n  %s\n", initPayload.Task)
	}
	if initPayload.Contracts != "" {
		fmt.Printf("\033[1;35m\n📜 CONTRATOS & ESPECIFICAÇÃO DE DADOS:\033[0m\n%s\n", initPayload.Contracts)
	}
	fmt.Println(strings.Repeat("─", 70))
	fmt.Println()

	// Goroutine para escutar sinais de teardown (dismiss) do Hub
	dismissChan := make(chan struct{})
	go func() {
		for scanner.Scan() {
			var msg hub.Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err == nil {
				if msg.Type == hub.MsgTypeDismiss {
					close(dismissChan)
					return
				}
			}
		}
	}()

	// 5. Execução do Agente Local
	var execErr error
	if preset == "agy" || preset == "overclock-ui" {
		promptInstruction := strings.TrimSpace(initPayload.Task)
		if initPayload.Contracts != "" {
			promptInstruction += "\n\nContratos de Dados:\n" + initPayload.Contracts
		}

		// Prioridade 1: overclock-ui para experiência rica com spinner, streaming, thinking e markdown
		uiPath, errLookUI := exec.LookPath("overclock-ui")
		if errLookUI != nil {
			home, _ := os.UserHomeDir()
			uiPath = home + "/.local/bin/overclock-ui"
		}

		agyPath, errLookAgy := exec.LookPath("agy")
		if errLookAgy != nil {
			home, _ := os.UserHomeDir()
			agyPath = home + "/.local/bin/agy"
		}

		if _, errStat := os.Stat(uiPath); errStat == nil {
			fmt.Printf("\033[1;35m⚡ Agente Ativo: Overclock UI (TUI com Streaming & Telemetria)\033[0m\n\n")
			var cmd *exec.Cmd
			if promptInstruction != "" {
				cmd = exec.Command(uiPath, "-p", promptInstruction, "--dangerously-skip-permissions")
			} else {
				cmd = exec.Command(uiPath)
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			execErr = cmd.Run()
		} else if _, errStat := os.Stat(agyPath); errStat == nil {
			fmt.Printf("\033[1;35m⚡ Agente Ativo: Antigravity CLI (agy nativo)\033[0m\n\n")
			var cmd *exec.Cmd
			if promptInstruction != "" {
				cmd = exec.Command(agyPath, "-p", promptInstruction, "--dangerously-skip-permissions")
			} else {
				cmd = exec.Command(agyPath)
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			execErr = cmd.Run()
		} else {
			fmt.Printf("⚠️ Binário 'overclock-ui' ou 'agy' não localizado. Abrindo bash na worktree...\n")
			cmd := exec.Command("bash")
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			execErr = cmd.Run()
		}
	} else {
		cmd := exec.Command("bash")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		execErr = cmd.Run()
	}

	// 6. Detecção de artefatos gerados na worktree
	artifacts := detectModifiedFiles()

	// 7. Notificação de Handoff
	status := "PASS"
	notes := "Tarefa executada com sucesso pelo worker."
	if execErr != nil {
		status = "FAIL"
		notes = fmt.Sprintf("Execução encerrou com erro: %v", execErr)
	}

	handoffPayload := hub.HandoffPayload{
		Status:    status,
		Artifacts: artifacts,
		Notes:     notes,
	}
	handoffBytes, _ := json.Marshal(handoffPayload)

	handoffMsg := hub.Message{
		Type:      hub.MsgTypeHandoff,
		WorkerID:  workerID,
		Payload:   handoffBytes,
		Timestamp: time.Now(),
	}
	data, _ := json.Marshal(handoffMsg)
	data = append(data, '\n')
	_, _ = conn.Write(data)

	// Sinal de Handoff Padronizado no terminal (Protocolo Maestro Framework)
	fmt.Printf("\n\033[1;32m[HANDOFF]\033[0m\n")
	fmt.Printf("- Tarefa: %s\n", workerID)
	fmt.Printf("- Artefatos Produzidos: %d arquivos (%s)\n", len(artifacts), strings.Join(artifacts, ", "))
	fmt.Printf("- Status dos Testes/Critérios: %s\n", status)
	fmt.Printf("- Notas de Entrega: %s\n\n", notes)

	// Teardown imediato (Zero tempo de inspeção estático)
	select {
	case <-dismissChan:
		fmt.Println("⚡ Sinal de dismiss recebido do Maestro.")
	case <-time.After(500 * time.Millisecond):
	}
}

func detectModifiedFiles() []string {
	cmd := exec.Command("git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 3 {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				files = append(files, parts[len(parts)-1])
			}
		}
	}
	return files
}
