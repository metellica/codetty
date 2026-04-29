好的，我已经充分理解了 ttyd 的架构。以下是完整的设计方案和核心实现代码。

---

## 架构分析

ttyd 现有协议非常清晰：
- **客户端→服务端**：`INPUT`('0'), `RESIZE`('1'), `PAUSE`('2'), `RESUME`('3'), `JSON_DATA`('{')
- **服务端→客户端**：`OUTPUT`('0'), `SET_WINDOW_TITLE`('1'), `SET_PREFERENCES`('2')

你的需求是**角色反转**：终端机器主动连接中心服务，而不是浏览器连接终端机器。

---

## 推荐方案：用 Go 全新实现（而不是修改 ttyd C 代码）

**为什么不直接改 ttyd？** ttyd 是 C + libwebsockets，加 TUI + 反向连接模式改动极大。用 Go 实现更清晰，且有一流的库：
- `bubbletea` + `lipgloss` → TUI
- `gorilla/websocket` → WebSocket
- `creack/pty` → PTY 管理

---

## 系统设计

```
┌─────────────────────────────────────────────────┐
│  ttyd-hub (TUI 服务端，运行在监控机上)             │
│  监听 :8080，接受多个 client 连接                  │
└───────────────┬───────────────┬─────────────────┘
     WebSocket  │    WebSocket  │   WebSocket
                ▼               ▼
         ┌──────────┐    ┌──────────┐
         │ ttyd-cli │    │ ttyd-cli │   ...
         │ machine1 │    │ machine2 │
         │  claude  │    │  claude  │
         └──────────┘    └──────────┘
```

**数据流：**
```
PTY Output → client → WS stream → hub → TUI 渲染
键盘输入 ← client ← WS stream ← hub ← TUI 捕获（全屏模式）
```

---

## 核心代码

### 1. 协议定义 (`protocol/protocol.go`)

```go name=protocol/protocol.go
package protocol

import "encoding/json"

type MsgType byte

const (
    // client → hub
    MsgOutput   MsgType = 'o' // PTY output data
    MsgRegister MsgType = 'r' // client registration

    // hub → client
    MsgInput  MsgType = 'i' // keyboard input to PTY
    MsgResize MsgType = 's' // resize PTY
)

type RegisterPayload struct {
    ID    string `json:"id"`
    Title string `json:"title"`
    Cols  uint16 `json:"cols"`
    Rows  uint16 `json:"rows"`
}

type ResizePayload struct {
    Cols uint16 `json:"cols"`
    Rows uint16 `json:"rows"`
}

func EncodeRegister(p RegisterPayload) ([]byte, error) {
    b, err := json.Marshal(p)
    if err != nil {
        return nil, err
    }
    return append([]byte{byte(MsgRegister)}, b...), nil
}

func EncodeOutput(data []byte) []byte {
    return append([]byte{byte(MsgOutput)}, data...)
}

func EncodeInput(data []byte) []byte {
    return append([]byte{byte(MsgInput)}, data...)
}

func EncodeResize(cols, rows uint16) ([]byte, error) {
    b, err := json.Marshal(ResizePayload{Cols: cols, Rows: rows})
    if err != nil {
        return nil, err
    }
    return append([]byte{byte(MsgResize)}, b...), nil
}
```

---

### 2. 客户端 (`cmd/ttyd-cli/main.go`)

```go name=cmd/ttyd-cli/main.go
package main

import (
    "flag"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/creack/pty"
    "github.com/gorilla/websocket"
    "yourmodule/protocol"
    "os/exec"
)

func main() {
    serverURL := flag.String("server", "ws://localhost:8080/ws", "hub server WebSocket URL")
    id := flag.String("id", mustHostname(), "client identifier")
    flag.Parse()
    args := flag.Args()
    if len(args) == 0 {
        log.Fatal("Usage: ttyd-cli --server ws://hub:8080/ws -- <command> [args...]")
    }

    conn, _, err := websocket.DefaultDialer.Dial(*serverURL, nil)
    if err != nil {
        log.Fatalf("connect to hub: %v", err)
    }
    defer conn.Close()

    // Get terminal size
    sz, _ := pty.GetsizeFull(os.Stdin)
    cols, rows := uint16(220), uint16(50) // default
    if sz != nil {
        cols, rows = sz.Cols, sz.Rows
    }

    // Register with hub
    regMsg, _ := protocol.EncodeRegister(protocol.RegisterPayload{
        ID:    *id,
        Title: fmt.Sprintf("%s@%s", args[0], mustHostname()),
        Cols:  cols,
        Rows:  rows,
    })
    conn.WriteMessage(websocket.BinaryMessage, regMsg)

    // Spawn the command in a PTY
    cmd := exec.Command(args[0], args[1:]...)
    ptmx, err := pty.Start(cmd)
    if err != nil {
        log.Fatalf("pty.Start: %v", err)
    }
    defer ptmx.Close()

    // PTY output → hub
    go func() {
        buf := make([]byte, 4096)
        for {
            n, err := ptmx.Read(buf)
            if n > 0 {
                conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeOutput(buf[:n]))
            }
            if err != nil {
                conn.WriteMessage(websocket.CloseMessage,
                    websocket.FormatCloseMessage(1000, "pty closed"))
                return
            }
        }
    }()

    // Hub → PTY input / resize
    go func() {
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                return
            }
            if len(msg) < 1 {
                continue
            }
            switch protocol.MsgType(msg[0]) {
            case protocol.MsgInput:
                ptmx.Write(msg[1:])
            case protocol.MsgResize:
                var p protocol.ResizePayload
                if json.Unmarshal(msg[1:], &p) == nil {
                    pty.Setsize(ptmx, &pty.Winsize{Cols: p.Cols, Rows: p.Rows})
                }
            }
        }
    }()

    // Wait for process exit or signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    doneCh := make(chan error, 1)
    go func() { doneCh <- cmd.Wait() }()

    select {
    case <-sigCh:
    case <-doneCh:
    }
}

func mustHostname() string {
    h, _ := os.Hostname()
    return h
}
```

---

### 3. TUI 服务端 (`cmd/ttyd-hub/main.go`)

```go name=cmd/ttyd-hub/main.go
package main

import (
    "flag"
    "log"
    "net/http"
    "sync"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/gorilla/websocket"
    "yourmodule/hub"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func main() {
    addr := flag.String("addr", ":8080", "listen address")
    flag.Parse()

    h := hub.New()

    // Start TUI in a goroutine
    go func() {
        p := tea.NewProgram(h.TUIModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
        h.SetProgram(p)
        if _, err := p.Run(); err != nil {
            log.Fatal(err)
        }
    }()

    // WebSocket endpoint
    http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            return
        }
        h.HandleConn(conn)
    })

    log.Printf("ttyd-hub listening on %s", *addr)
    log.Fatal(http.ListenAndServe(*addr, nil))
}
```

---

### 4. Hub 核心逻辑 (`hub/hub.go`)

```go name=hub/hub.go
package hub

import (
    "encoding/json"
    "sync"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/gorilla/websocket"
    "yourmodule/protocol"
)

// ClientSession represents one connected terminal client
type ClientSession struct {
    ID      string
    Title   string
    Cols    uint16
    Rows    uint16
    conn    *websocket.Conn
    screen  []byte   // last rendered screen snapshot
    mu      sync.Mutex
    hub     *Hub
}

func (c *ClientSession) SendInput(data []byte) {
    c.conn.WriteMessage(websocket.BinaryMessage, protocol.EncodeInput(data))
}

func (c *ClientSession) SendResize(cols, rows uint16) {
    msg, _ := protocol.EncodeResize(cols, rows)
    c.conn.WriteMessage(websocket.BinaryMessage, msg)
    c.Cols, c.Rows = cols, rows
}

// Hub manages all sessions and the TUI program
type Hub struct {
    mu       sync.RWMutex
    sessions map[string]*ClientSession
    program  *tea.Program
}

func New() *Hub {
    return &Hub{sessions: make(map[string]*ClientSession)}
}

func (h *Hub) SetProgram(p *tea.Program) {
    h.program = p
}

func (h *Hub) HandleConn(conn *websocket.Conn) {
    var sess *ClientSession

    defer func() {
        if sess != nil {
            h.removeSession(sess.ID)
        }
        conn.Close()
    }()

    for {
        _, msg, err := conn.ReadMessage()
        if err != nil || len(msg) < 1 {
            return
        }

        switch protocol.MsgType(msg[0]) {
        case protocol.MsgRegister:
            var p protocol.RegisterPayload
            if err := json.Unmarshal(msg[1:], &p); err != nil {
                return
            }
            sess = &ClientSession{
                ID: p.ID, Title: p.Title,
                Cols: p.Cols, Rows: p.Rows,
                conn: conn, hub: h,
            }
            h.addSession(sess)

        case protocol.MsgOutput:
            if sess == nil {
                return
            }
            data := msg[1:]
            sess.mu.Lock()
            // Append to screen buffer (simplified; real impl uses vt100 parser)
            sess.screen = append(sess.screen, data...)
            if len(sess.screen) > 64*1024 {
                sess.screen = sess.screen[len(sess.screen)-64*1024:]
            }
            sess.mu.Unlock()

            if h.program != nil {
                h.program.Send(ScreenUpdateMsg{ID: sess.ID, Data: data})
            }
        }
    }
}

func (h *Hub) addSession(s *ClientSession) {
    h.mu.Lock()
    h.sessions[s.ID] = s
    h.mu.Unlock()
    if h.program != nil {
        h.program.Send(ClientConnectedMsg{Session: s})
    }
}

func (h *Hub) removeSession(id string) {
    h.mu.Lock()
    delete(h.sessions, id)
    h.mu.Unlock()
    if h.program != nil {
        h.program.Send(ClientDisconnectedMsg{ID: id})
    }
}

func (h *Hub) Sessions() []*ClientSession {
    h.mu.RLock()
    defer h.mu.RUnlock()
    out := make([]*ClientSession, 0, len(h.sessions))
    for _, s := range h.sessions {
        out = append(out, s)
    }
    return out
}

// Tea messages
type ClientConnectedMsg struct{ Session *ClientSession }
type ClientDisconnectedMsg struct{ ID string }
type ScreenUpdateMsg struct {
    ID   string
    Data []byte
}
```

---

### 5. TUI Model (`hub/tui.go`)

```go name=hub/tui.go
package hub

import (
    "fmt"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    "golang.org/x/term"
)

// ── Styles ──────────────────────────────────────────────
var (
    panelStyle = lipgloss.NewStyle().
            Border(lipgloss.RoundedBorder()).
            BorderForeground(lipgloss.Color("240"))

    focusedPanelStyle = lipgloss.NewStyle().
                Border(lipgloss.RoundedBorder()).
                BorderForeground(lipgloss.Color("86"))

    titleStyle = lipgloss.NewStyle().
            Foreground(lipgloss.Color("86")).Bold(true)

    statusBar = lipgloss.NewStyle().
            Background(lipgloss.Color("235")).
            Foreground(lipgloss.Color("250")).
            Padding(0, 1)
)

// ── Model ────────────────────────────────────────────────
type tuiModel struct {
    hub        *Hub
    sessions   []*ClientSession // ordered list
    screens    map[string][]string // id → lines
    selected   int
    fullscreen bool
    width      int
    height     int
}

func (h *Hub) TUIModel() tea.Model {
    w, ht, _ := term.GetSize(0)
    return &tuiModel{
        hub:     h,
        screens: make(map[string][]string),
        width:   w,
        height:  ht,
    }
}

func (m *tuiModel) Init() tea.Cmd { return nil }

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {

    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height

    case ClientConnectedMsg:
        m.sessions = m.hub.Sessions()
        m.screens[msg.Session.ID] = []string{}

    case ClientDisconnectedMsg:
        m.sessions = m.hub.Sessions()
        delete(m.screens, msg.ID)
        if m.selected >= len(m.sessions) {
            m.selected = max(0, len(m.sessions)-1)
        }

    case ScreenUpdateMsg:
        // Append new output lines (naive: split on newline)
        lines := strings.Split(string(msg.Data), "\n")
        existing := m.screens[msg.ID]
        existing = append(existing, lines...)
        if len(existing) > 500 {
            existing = existing[len(existing)-500:]
        }
        m.screens[msg.ID] = existing

    case tea.KeyMsg:
        if m.fullscreen {
            // Forward all keys to focused session
            if msg.Type == tea.KeyEsc || (msg.Type == tea.KeyCtrlA && msg.Alt) {
                m.fullscreen = false
                return m, nil
            }
            if len(m.sessions) > 0 {
                m.sessions[m.selected].SendInput([]byte(msg.String()))
            }
            return m, nil
        }
        // Grid navigation
        switch msg.String() {
        case "q", "ctrl+c":
            return m, tea.Quit
        case "tab", "l", "right":
            if len(m.sessions) > 0 {
                m.selected = (m.selected + 1) % len(m.sessions)
            }
        case "shift+tab", "h", "left":
            if len(m.sessions) > 0 {
                m.selected = (m.selected - 1 + len(m.sessions)) % len(m.sessions)
            }
        case "enter", "f":
            if len(m.sessions) > 0 {
                m.fullscreen = true
                // Resize PTY to match our terminal
                sess := m.sessions[m.selected]
                sess.SendResize(uint16(m.width), uint16(m.height-2))
            }
        }
    }
    return m, nil
}

func (m *tuiModel) View() string {
    if len(m.sessions) == 0 {
        return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
            titleStyle.Render("⬡ ttyd-hub")+"\n\n"+
                "Waiting for clients to connect...\n\n"+
                lipgloss.NewStyle().Foreground(lipgloss.Color("240")).
                    Render("Run: ttyd-cli --server ws://<this-host>:8080/ws -- <cmd>"))
    }

    if m.fullscreen && m.selected < len(m.sessions) {
        return m.fullscreenView()
    }
    return m.gridView()
}

func (m *tuiModel) gridView() string {
    n := len(m.sessions)
    cols := clamp(n, 1, 3) // max 3 columns
    rows := (n + cols - 1) / cols

    panelW := (m.width / cols) - 2
    panelH := ((m.height - 2) / rows) - 2

    var rowViews []string
    for r := 0; r < rows; r++ {
        var colViews []string
        for c := 0; c < cols; c++ {
            idx := r*cols + c
            if idx >= n {
                colViews = append(colViews, strings.Repeat(" ", panelW+4))
                continue
            }
            sess := m.sessions[idx]
            content := m.renderScreen(sess.ID, panelW, panelH)
            title := fmt.Sprintf(" [%d] %s ", idx+1, sess.Title)

            style := panelStyle
            if idx == m.selected {
                style = focusedPanelStyle
                title = fmt.Sprintf(" ▶ [%d] %s ", idx+1, sess.Title)
            }

            panel := style.Width(panelW).Height(panelH).Render(
                titleStyle.Render(title) + "\n" + content,
            )
            colViews = append(colViews, panel)
        }
        rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
    }

    grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
    help := statusBar.Width(m.width).Render(
        "Tab/←→: navigate  Enter/f: fullscreen  q: quit  " +
            fmt.Sprintf("clients: %d", n),
    )
    return lipgloss.JoinVertical(lipgloss.Left, grid, help)
}

func (m *tuiModel) fullscreenView() string {
    sess := m.sessions[m.selected]
    content := m.renderScreen(sess.ID, m.width-2, m.height-3)
    header := focusedPanelStyle.Width(m.width - 2).Render(
        titleStyle.Render(fmt.Sprintf(" FULLSCREEN: %s ", sess.Title)) + "\n" + content,
    )
    help := statusBar.Width(m.width).Render("ESC or Ctrl+A: back to grid  (all keys forwarded to terminal)")
    return lipgloss.JoinVertical(lipgloss.Left, header, help)
}

func (m *tuiModel) renderScreen(id string, w, h int) string {
    lines := m.screens[id]
    // take last h lines
    if len(lines) > h {
        lines = lines[len(lines)-h:]
    }
    var sb strings.Builder
    for i, l := range lines {
        if i > 0 {
            sb.WriteRune('\n')
        }
        if len(l) > w {
            l = l[:w]
        }
        sb.WriteString(l)
    }
    return sb.String()
}

func clamp(v, lo, hi int) int {
    if v < lo {
        return lo
    }
    if v > hi {
        return hi
    }
    return v
}

func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}
```

---

### 6. `go.mod` 依赖

```go name=go.mod
module yourmodule

go 1.21

require (
    github.com/charmbracelet/bubbletea v0.25.0
    github.com/charmbracelet/lipgloss v0.9.1
    github.com/creack/pty v1.1.21
    github.com/gorilla/websocket v1.5.1
    golang.org/x/term v0.16.0
)
```

---

## 使用方式

```bash
# 1. 启动服务端（监控机）
ttyd-hub --addr :8080

# 2. 每台终端机器上启动客户端
ttyd-cli --server ws://hub-ip:8080/ws --id machine1 -- claude
ttyd-cli --server ws://hub-ip:8080/ws --id machine2 -- claude
```

---

## TUI 效果示意

```
╭──────────────────╮╭──────────────────╮╭──────────────────╮
│ ▶ [1] machine1   ││  [2] machine2    ││  [3] machine3    │
│ > analyzing...   ││ > running tests  ││ > idle           │
│ ✓ step 1/5       ││ ● test_auth      ││ $                │
│ ✓ step 2/5       ││ ● test_db        ││                  │
╰──────────────────╯╰──────────────────╯╰──────────────────╯
 Tab/←→: navigate  Enter/f: fullscreen  q: quit  clients: 3
```

**Enter 后全屏：**
```
╭─ FULLSCREEN: machine1 ──────────────────────────────────╮
│ > Claude is analyzing your codebase...                   │
│ ✓ Found 42 files                                         │
│ ✓ Parsed dependencies                                    │
│ > Generating refactoring plan...                         │
│ _                                                        │
╰──────────────────────────────────────────────────────────╯
 ESC or Ctrl+A: back to grid  (all keys forwarded to terminal)
```

---

## 进阶改进建议

| 功能 | 实现方式 |
|------|---------|
| **真实 VT100 渲染** | 集成 [`github.com/nicowillis/vt10x`](https://github.com/hinshun/vt10x) 解析 ANSI 转义序列 |
| **持久化录制** | 每个 session 同时写 asciinema 格式文件 |


### TODO
| 功能 | 实现方式 |
|------|---------|
| **Web 界面** | 在 hub 上同时暴露 HTTP，复用 ttyd 的 xterm.js 前端 |
| **鉴权** | WebSocket 握手时验证 token |
| **断线重连** | 客户端指数退避重连，hub 保留 session 状态 |
