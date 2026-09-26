package hyprland

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
)

// TerminalEmulator represents supported terminal emulators for tiling panes.
type TerminalEmulator string

const (
	TerminalKitty     TerminalEmulator = "kitty"
	TerminalFoot      TerminalEmulator = "foot"
	TerminalAlacritty TerminalEmulator = "alacritty"
	TerminalGhostty   TerminalEmulator = "ghostty"
)

// WindowOptions specifies the parameters for spawning a new worker window in Hyprland.
type WindowOptions struct {
	Class     string           // e.g. "overclock-worker"
	Title     string           // e.g. "⚡ Overclock: worker-backend"
	Directory string           // working directory (e.g. .worktrees/auth-backend)
	Command   string           // shell command to execute inside the terminal
	Terminal  TerminalEmulator // e.g. TerminalKitty
	Workspace string           // e.g. "current" or workspace index
	Silent    bool             // If true, spawn window silently without switching workspace or stealing focus
}

// Client represents a Hyprland window client as returned by `hyprctl -j clients`.
type Client struct {
	Address   string `json:"address"`
	Mapped    bool   `json:"mapped"`
	Hidden    bool   `json:"hidden"`
	At        [2]int `json:"at"`
	Size      [2]int `json:"size"`
	Workspace struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
	Class            string `json:"class"`
	Title            string `json:"title"`
	InitialClass     string `json:"initialClass"`
	InitialTitle     string `json:"initialTitle"`
	PID              int    `json:"pid"`
	XWayland         bool   `json:"xwayland"`
	Pinned           bool   `json:"pinned"`
	Fullscreen       int    `json:"fullscreen"`
	FullscreenClient int    `json:"fullscreenClient"`
}

// Controller exposes operations against the Hyprland Wayland compositor via `hyprctl`.
type Controller struct {
	hyprctlPath string
	execCmd     func(name string, args ...string) ([]byte, error)
}

// NewController creates a new Hyprland controller.
func NewController() *Controller {
	path, _ := exec.LookPath("hyprctl")
	return &Controller{
		hyprctlPath: path,
		execCmd: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
	}
}

// IsAvailable checks whether Hyprland is currently running and accessible.
func (c *Controller) IsAvailable() bool {
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		return false
	}
	if c.hyprctlPath == "" {
		return false
	}
	out, err := c.execCmd(c.hyprctlPath, "version")
	if err != nil {
		return false
	}
	return len(out) > 0
}

// GetProcessAncestors returns the list of ancestor PIDs for the given start PID.
func GetProcessAncestors(startPID int) []int {
	var pids []int
	curr := startPID
	for curr > 1 {
		pids = append(pids, curr)
		statPath := fmt.Sprintf("/proc/%d/stat", curr)
		data, err := os.ReadFile(statPath)
		if err != nil {
			break
		}
		str := string(data)
		idx := strings.LastIndex(str, ")")
		if idx == -1 || idx+2 >= len(str) {
			break
		}
		fields := strings.Fields(str[idx+2:])
		if len(fields) < 2 {
			break
		}
		var ppid int
		if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil || ppid <= 1 || ppid == curr {
			break
		}
		curr = ppid
	}
	return pids
}

// GetCallerWorkspace determines the exact workspace ID of the Maestro terminal window
// by inspecting the ancestor process tree against active Hyprland window clients.
func (c *Controller) GetCallerWorkspace() string {
	if !c.IsAvailable() {
		return "1"
	}

	clients, err := c.ListClients()
	if err == nil && len(clients) > 0 {
		ancestors := GetProcessAncestors(os.Getpid())
		for _, client := range clients {
			for _, pid := range ancestors {
				if client.PID == pid && client.Workspace.ID > 0 {
					return fmt.Sprintf("%d", client.Workspace.ID)
				}
			}
		}

		// Fallback: Check if any client has class 'overclock-maestro'
		for _, client := range clients {
			if client.Class == "overclock-maestro" && client.Workspace.ID > 0 {
				return fmt.Sprintf("%d", client.Workspace.ID)
			}
		}
	}

	// Fallback to active workspace
	if ws, err := c.GetActiveWorkspace(); err == nil && ws != "" && ws != "current" {
		return ws
	}

	return "1"
}

// GetActiveWorkspace returns the ID of the current active workspace.
func (c *Controller) GetActiveWorkspace() (string, error) {
	if !c.IsAvailable() {
		return "1", nil
	}
	out, err := c.execCmd(c.hyprctlPath, "activeworkspace", "-j")
	if err != nil {
		return "current", err
	}
	var data struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(out, &data); err != nil || data.ID == 0 {
		return "current", nil
	}
	return fmt.Sprintf("%d", data.ID), nil
}

// SetLayout dynamically alters the Hyprland tiling layout (e.g. "master" or "scrolling").
func (c *Controller) SetLayout(layoutName string) error {
	if !c.IsAvailable() {
		return nil
	}
	luaCmd := fmt.Sprintf("hl.config({ general = { layout = %q } })", layoutName)
	out, err := c.execCmd(c.hyprctlPath, "eval", luaCmd)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}
	return err
}

// GetCurrentLayout returns the active global layout (e.g. "scrolling" or "master").
func (c *Controller) GetCurrentLayout() string {
	if !c.IsAvailable() {
		return "scrolling"
	}
	out, err := c.execCmd(c.hyprctlPath, "getoption", "general:layout", "-j")
	if err != nil {
		return "scrolling"
	}
	var data struct {
		Str string `json:"str"`
	}
	if err := json.Unmarshal(out, &data); err == nil && data.Str != "" {
		return data.Str
	}
	return "scrolling"
}

// BuildTerminalCommand constructs the terminal launcher command string.
func BuildTerminalCommand(opts WindowOptions) (string, []string, error) {
	term := opts.Terminal
	if term == "" {
		term = TerminalKitty
	}

	class := opts.Class
	if class == "" {
		class = "overclock-worker"
	}

	title := opts.Title
	if title == "" {
		title = "⚡ Overclock Worker"
	}

	dir := opts.Directory
	if dir == "" {
		dir = "."
	}

	switch term {
	case TerminalKitty:
		// kitty --class <class> --title <title> --directory <dir> sh -c '<command>'
		args := []string{
			"--class", class,
			"--title", title,
			"--directory", dir,
		}
		if opts.Command != "" {
			args = append(args, "sh", "-c", opts.Command)
		}
		return "kitty", args, nil

	case TerminalFoot:
		args := []string{
			"--app-id", class,
			"--title", title,
			"--working-directory", dir,
		}
		if opts.Command != "" {
			args = append(args, "sh", "-c", opts.Command)
		}
		return "foot", args, nil

	case TerminalAlacritty:
		args := []string{
			"--class", class,
			"--title", title,
			"--working-directory", dir,
		}
		if opts.Command != "" {
			args = append(args, "-e", "sh", "-c", opts.Command)
		}
		return "alacritty", args, nil

	case TerminalGhostty:
		args := []string{
			"--class=" + class,
			"--title=" + title,
			"--working-directory=" + dir,
		}
		if opts.Command != "" {
			args = append(args, "-e", "sh", "-c", opts.Command)
		}
		return "ghostty", args, nil

	default:
		return "", nil, fmt.Errorf("emulador de terminal '%s' não suportado", term)
	}
}

// SpawnWindow requests Hyprland to launch a new terminal window in the active workspace.
func (c *Controller) SpawnWindow(opts WindowOptions) error {
	bin, args, err := BuildTerminalCommand(opts)
	if err != nil {
		return err
	}

	fullTermCmd := bin + " " + strings.Join(quoteArgs(args), " ")

	// Determina o workspace alvo (o workspace onde o Maestro reside)
	targetWs := opts.Workspace
	if targetWs == "" || targetWs == "current" {
		targetWs = c.GetCallerWorkspace()
	}

	// Se Hyprland estiver ativo, despacha no workspace correto do Maestro sem roubar foco inicial
	if c.IsAvailable() {
		rule := fmt.Sprintf("workspace %s; noinitialfocus", targetWs)
		if opts.Silent {
			rule = fmt.Sprintf("workspace %s silent; noinitialfocus", targetWs)
		}
		dispatchWithWs := fmt.Sprintf("[%s] %s", rule, fullTermCmd)

		// 1. Tenta Hyprland 0.56+ via hyprctl eval com hl.dispatch(hl.dsp.exec_cmd(...))
		luaEvalCmd := fmt.Sprintf("return hl.dispatch(hl.dsp.exec_cmd(%q))", dispatchWithWs)
		out, err := c.execCmd(c.hyprctlPath, "eval", luaEvalCmd)
		if err == nil && !strings.Contains(string(out), "error:") {
			return nil
		}

		// 2. Tenta Hyprland 0.56+ via hyprctl repl
		luaReplCmd := fmt.Sprintf("hl.dispatch(hl.dsp.exec_cmd(%q))", dispatchWithWs)
		out, err = c.execCmd(c.hyprctlPath, "repl", luaReplCmd)
		if err == nil && !strings.Contains(string(out), "error:") {
			return nil
		}

		// 3. Tenta sintaxe legada de dispatch (Hyprland <0.56)
		out, err = c.execCmd(c.hyprctlPath, "dispatch", "exec", dispatchWithWs)
		if err == nil && !strings.Contains(string(out), "error:") {
			return nil
		}
	}

	// 4. Fallback resiliente: execução direta do emulador de terminal via exec.Command
	cmd := exec.Command(bin, args...)
	if opts.Directory != "" {
		cmd.Dir = opts.Directory
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("falha ao iniciar terminal '%s' diretamente: %w", bin, err)
	}
	return nil
}

// CloseWindowByTitle closes the Hyprland window whose title matches the regex/string.
func (c *Controller) CloseWindowByTitle(title string) error {
	if !c.IsAvailable() {
		return nil
	}

	// 1. Tenta via Lua REPL (Hyprland 0.56+)
	luaCode := fmt.Sprintf(`local wins = hl.get_windows(); for _, win in ipairs(wins) do if win.title == %q or string.find(win.title, %q) then hl.dispatch(hl.dsp.window.close({ window = win })) end end`, title, title)
	out, err := c.execCmd(c.hyprctlPath, "repl", luaCode)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}

	// 2. Tenta via dispatch legado (Hyprland <0.56)
	pattern := fmt.Sprintf("title:^%s$", regexp.QuoteMeta(title))
	out, err = c.execCmd(c.hyprctlPath, "dispatch", "closewindow", pattern)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}

	// 3. Fallback: busca por ListClients() e envia SIGTERM para o PID
	clients, _ := c.ListClients()
	for _, client := range clients {
		if (client.Title == title || strings.Contains(client.Title, title)) && client.PID > 0 {
			if proc, err := os.FindProcess(client.PID); err == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
		}
	}
	return nil
}

// CloseWindowByClass closes windows with the specified window class.
func (c *Controller) CloseWindowByClass(class string) error {
	if !c.IsAvailable() {
		return nil
	}

	// 1. Tenta via Lua REPL (Hyprland 0.56+)
	luaCode := fmt.Sprintf(`local wins = hl.get_windows(); for _, win in ipairs(wins) do if win.class == %q or string.find(win.class, %q) then hl.dispatch(hl.dsp.window.close({ window = win })) end end`, class, class)
	out, err := c.execCmd(c.hyprctlPath, "repl", luaCode)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}

	// 2. Tenta via dispatch legado (Hyprland <0.56)
	pattern := fmt.Sprintf("class:^%s$", regexp.QuoteMeta(class))
	out, err = c.execCmd(c.hyprctlPath, "dispatch", "closewindow", pattern)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}

	// 3. Fallback: busca por ListClients() e envia SIGTERM para o PID
	clients, _ := c.ListClients()
	for _, client := range clients {
		if (client.Class == class || strings.Contains(client.Class, class)) && client.PID > 0 {
			if proc, err := os.FindProcess(client.PID); err == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
		}
	}
	return nil
}

// FocusWindow focuses the window matching the given title.
func (c *Controller) FocusWindow(title string) error {
	if !c.IsAvailable() {
		return nil
	}

	// 1. Tenta via Lua REPL (Hyprland 0.56+)
	luaCode := fmt.Sprintf(`local wins = hl.get_windows(); for _, win in ipairs(wins) do if win.title == %q or string.find(win.title, %q) then hl.dispatch(hl.dsp.focus({ window = win })) return end end`, title, title)
	out, err := c.execCmd(c.hyprctlPath, "repl", luaCode)
	if err == nil && !strings.Contains(string(out), "error:") {
		return nil
	}

	// 2. Tenta via dispatch legado (Hyprland <0.56)
	pattern := fmt.Sprintf("title:^%s$", regexp.QuoteMeta(title))
	_, err = c.execCmd(c.hyprctlPath, "dispatch", "focuswindow", pattern)
	return err
}

// ListClients returns the active window clients tracked by Hyprland.
func (c *Controller) ListClients() ([]Client, error) {
	if !c.IsAvailable() {
		return nil, nil
	}

	out, err := c.execCmd(c.hyprctlPath, "-j", "clients")
	if err != nil {
		return nil, fmt.Errorf("falha ao listar janelas do Hyprland: %s (%w)", string(out), err)
	}

	var clients []Client
	if err := json.Unmarshal(out, &clients); err != nil {
		return nil, fmt.Errorf("falha ao parsear clientes do Hyprland: %w", err)
	}

	return clients, nil
}

func quoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'$`\\") {
			escaped := strings.ReplaceAll(arg, `"`, `\"`)
			quoted[i] = `"` + escaped + `"`
		} else {
			quoted[i] = arg
		}
	}
	return quoted
}
