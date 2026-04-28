# codetty — 终端监控系统设计文档

## 概述

codetty 是一个终端监控系统，用于集中查看和操控多台机器上运行的终端程序（如 claude CLI）。核心设计是**角色反转**：终端机器主动连接中心 hub，而不是传统的浏览器连接终端。

### 核心价值

在一个 TUI 界面中同时监控 N 台机器的终端输出，需要时切入全屏交互。

### 技术选型

| 组件 | 选择 | 理由 |
|------|------|------|
| 语言 | Go | 交叉编译简单，并发原生支持，单二进制部署 |
| TUI | charmbracelet/bubbletea + lipgloss | Go TUI 生态标准 |
| WebSocket | gorilla/websocket | 成熟稳定 |
| PTY | creack/pty | Go PTY 管理的事实标准 |
| VT100 解析 | hinshun/vt10x | Go 原生 VT100 解析器，支持颜色/光标/滚动 |

---

## 架构

```
┌─────────────────────────────────────────────────┐
│  codetty-hub (TUI 服务端，运行在监控机上)          │
│  监听 :8080，接受多个 client 连接                  │
│  每个 session 维护一个 vt10x 虚拟终端               │
└───────────────┬───────────────┬─────────────────┘
     WebSocket  │    WebSocket  │   WebSocket
                ▼               ▼
         ┌──────────┐    ┌──────────┐
         │codetty-  │    │codetty-  │   ...
         │cli       │    │cli       │
         │machine1  │    │machine2  │
         └──────────┘    └──────────┘
```

### 数据流

```
PTY Output → cli → WS binary → hub → vt10x 解析 → TUI 渲染
键盘输入 ← cli ← WS binary ← hub ← TUI/raw stdin 捕获
```

---

## 协议

单字节类型前缀 + payload 的二进制协议。所有消息通过 WebSocket binary frame 传输。

### 消息类型

| 方向 | 类型 | 前缀字节 | Payload |
|------|------|---------|---------|
| cli → hub | Register | `'r'` | JSON: `{id, title, cols, rows}` |
| cli → hub | Output | `'o'` | raw PTY bytes |
| hub → cli | Input | `'i'` | raw keyboard bytes |
| hub → cli | Resize | `'s'` | JSON: `{cols, rows}` |

### Register Payload

```json
{
  "id": "machine1",
  "title": "claude@machine1",
  "cols": 220,
  "rows": 50
}
```

---

## 组件设计

### 项目结构

```
codetty/
├── protocol/              # 消息类型定义和编解码
│   └── protocol.go
├── hub/                   # Hub 核心逻辑
│   ├── hub.go             # 连接管理、session 生命周期
│   ├── session.go         # ClientSession + vt10x 虚拟终端
│   ├── writer.go          # channel-based WebSocket writer
│   └── tui.go             # bubbletea TUI (网格+全屏)
├── cmd/
│   ├── codetty-hub/
│   │   └── main.go        # Hub 入口
│   └── codetty-cli/
│       └── main.go        # Client 入口
├── go.mod
└── go.sum
```

---

### 1. protocol 包

与 Design.md 中的设计一致。定义 4 种消息类型和编解码函数。

- `MsgRegister ('r')` — 客户端注册
- `MsgOutput ('o')` — PTY 输出
- `MsgInput ('i')` — 键盘输入
- `MsgResize ('s')` — 终端尺寸变更

---

### 2. codetty-cli（客户端）

**职责：**
1. 建立 WebSocket 连接到 hub
2. 启动 PTY 执行用户指定的命令
3. 双向桥接：PTY output → hub，hub input → PTY
4. 断线后指数退避重连

**启动命令：**
```bash
codetty-cli --server ws://hub-ip:8080/ws --id machine1 -- claude
```

**重连策略：**
- 初始延迟：1 秒
- 每次失败延迟翻倍
- 最大延迟：30 秒
- 成功连接后重置延迟
- 重连时重新发送 Register 消息
- PTY 进程退出时不重连，客户端整体退出

**关键行为：**
- 客户端本身不需要是终端（可以作为后台进程运行）
- 默认终端尺寸 220x50，如果 stdin 是终端则读取实际尺寸
- 读取 PTY 输出的 buffer 大小 4096 bytes
- PTY 关闭时发送 WebSocket close frame

---

### 3. Hub — 连接管理 (hub.go)

**职责：**
1. 监听 WebSocket 连接
2. 接收 Register 消息，创建 ClientSession
3. 接收 Output 消息，写入 session 的 vt10x 终端
4. 客户端断开时清理 session
5. 通过 bubbletea message 通知 TUI 状态变更

**连接处理流程：**
1. 升级 HTTP → WebSocket
2. 等待第一条消息必须是 Register
3. 创建 ClientSession，启动 write goroutine
4. 循环读取消息直到连接关闭
5. 关闭时移除 session，关闭 write channel

---

### 4. Hub — Session (session.go)

**ClientSession 结构：**

```go
type ClientSession struct {
    ID    string
    Title string
    Cols  uint16
    Rows  uint16
    conn  *websocket.Conn
    vt    *vt10x.VT          // 虚拟终端，唯一的终端状态
    writeCh chan []byte        // 写通道，串行化 WebSocket 写操作
    hub   *Hub
}
```

**关键设计决策：**

- **vt10x 是唯一的终端状态存储**。所有 PTY 输出写入 vt10x，TUI 渲染时从 vt10x 读取当前屏幕内容。没有额外的 screen buffer 或 lines 数组。
- **write channel** 串行化所有写操作（Input、Resize），避免 gorilla/websocket 并发写 panic。channel buffer 大小 256。
- write goroutine 在 channel 关闭时退出。

---

### 5. Hub — WebSocket Writer (writer.go)

**职责：** 从 channel 读取消息，串行写入 WebSocket。

```
SendInput(data) → writeCh ← writeLoop() → conn.WriteMessage()
SendResize(c,r) → writeCh ←┘
```

- channel 满时丢弃消息并记录日志（防止慢客户端阻塞 hub）
- write goroutine 在 channel 关闭或写错误时退出

---

### 6. Hub — TUI (tui.go)

**两种模式：**

#### 网格模式（Grid View）

- 显示所有连接的 session，最多 3 列
- 每个面板显示 session title + 终端内容最后 N 行
- 当前选中的面板有高亮边框
- 导航：Tab/←→ 切换焦点，Enter/f 进入全屏，q 退出

**网格渲染：** 对每个 session，调用 `vt.String()` 获取当前虚拟终端内容，截取到面板尺寸后显示。

#### 全屏模式（Fullscreen View）

- 选中的 session 占满整个终端
- 进入全屏时：
  1. 向对应客户端发送 Resize 消息，PTY 尺寸匹配当前 hub 终端
  2. 调用 `program.ReleaseTerminal()` 暂停 bubbletea 对 stdin 的读取
  3. 自行将 stdin 切到 raw mode（`term.MakeRaw()`）
  4. 启动一个 goroutine 直接从 stdin 读取 raw bytes 并转发给客户端
- 退出全屏：ESC 双击或 Ctrl+A（从 raw stream 中检测）
  1. 停止 raw 输入 goroutine
  2. 恢复终端模式（`term.Restore()`）
  3. 调用 `program.RestoreTerminal()` 让 bubbletea 重新接管输入
  4. 向客户端发送 Resize 恢复面板尺寸

**全屏退出热键设计：**
- 检测连续两次 ESC（间隔 < 300ms），或 Ctrl+A
- 单次 ESC 正常透传给远端终端

**bubbletea Tea Messages：**

| Message | 触发时机 |
|---------|---------|
| `ClientConnectedMsg{Session}` | 新客户端注册 |
| `ClientDisconnectedMsg{ID}` | 客户端断开 |
| `ScreenUpdateMsg{ID}` | 收到 Output 数据 |

---

## 断线重连

### 客户端重连

```
连接失败或断开
  → 等待 delay（初始 1s）
  → 尝试重新连接
  → 成功：重置 delay 为 1s，重新发送 Register
  → 失败：delay = min(delay * 2, 30s)，重试
```

### Hub 端行为

- 客户端断开时立即移除 session
- 客户端重连时创建新 session（ID 相同则替换旧的）
- vt10x 终端状态从零开始（重连后看到的是新的终端输出）
- 重连期间客户端 PTY 继续运行，产生的输出在 WS 恢复后正常发送（依赖 PTY buffer，不做额外缓存）

---

## 错误处理

| 场景 | 处理方式 |
|------|---------|
| Hub WebSocket 升级失败 | 返回 HTTP 错误，记录日志 |
| 客户端首条消息非 Register | 关闭连接 |
| PTY 启动失败 | 客户端 log.Fatal 退出 |
| PTY 进程退出 | 客户端发送 close frame 并退出（不重连） |
| WebSocket 写失败 | 关闭连接，触发重连（客户端侧） |
| write channel 满 | 丢弃消息，记录警告 |

---

## 使用方式

```bash
# 编译
go build -o codetty-hub ./cmd/codetty-hub
go build -o codetty-cli ./cmd/codetty-cli

# 启动 Hub（监控机）
./codetty-hub --addr :8080

# 启动客户端（每台终端机器）
./codetty-cli --server ws://hub-ip:8080/ws --id machine1 -- claude
./codetty-cli --server ws://hub-ip:8080/ws --id machine2 -- bash
```

---

## 不在 MVP 范围内

以下功能明确排除在首次实现之外：

- **Web 界面** — 后续迭代，可复用 xterm.js
- **Token 鉴权** — 内网使用暂不需要
- **持久化录制** — 后续迭代（asciinema 格式）
- **Session 持久化** — 重连后不恢复旧终端状态
