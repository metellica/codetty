# CodeTTY

反向连接的终端多路复用监控系统。多台远程机器主动连接中心服务，在 TUI 界面中以网格视图实时展示所有终端会话，支持全屏交互操控。

## 架构

```
┌───────────────────────────────────────┐
│  codetty-hub (TUI 服务端，监控机)       │
│  监听 :8080，接受多个 cli 连接          │
└──────────┬──────────┬─────────────────┘
  Secure WebSocket│ Secure WebSocket│  Secure WebSocket
           ▼          ▼
     ┌──────────┐ ┌──────────┐
     │codetty-cli│ │codetty-cli│  ...
     │ machine1 │ │ machine2 │
     └──────────┘ └──────────┘
```

**数据流（mTLS 加密）：**

```
PTY 输出 → cli → Secure WebSocket (mTLS) → hub → TUI 渲染
键盘输入 ← cli ← Secure WebSocket (mTLS) ← hub ← TUI 捕获
```

## 功能

- **网格视图** — 多终端会话以自适应网格排列，实时预览每个终端输出
- **全屏模式** — 选中会话后按 Enter 进入全屏交互，键盘输入直接转发到远程 PTY
- **断线重连** — cli 端指数退避自动重连，重连后发送屏幕快照恢复显示
- **屏幕快照** — 基于 vt10x 终端仿真器维护屏幕状态，重连时无缝恢复
- **鼠标支持** — 网格视图中可鼠标点击选择会话
- **键盘导航** — Tab/Shift+Tab/方向键切换会话，Enter/f 全屏，ESC 返回网格
- **自动身份识别** — cli 根据工作目录、主机名、PID 自动生成唯一标识
- **焦点事件过滤** — cli 过滤终端焦点事件 (CSI I/O)，避免干扰远程进程
- **自定义协议** — 基于 Secure WebSocket (mTLS) 的轻量二进制协议，支持注册、输出、输入、调整大小、屏幕重放
- **互相TLS（mTLS）** — 默认启用证书验证，确保 hub 和 cli 之间的通信加密和身份验证
- **跨平台 PTY** — Linux/macOS 使用 Unix PTY，Windows 使用 ConPTY

## 快速开始

### 构建

```bash
./build.sh
```

```powershell
.\build.bat
```

生成两个二进制文件：Unix-like 平台为 `codetty-hub` 和 `codetty-cli`，Windows 为 `codetty-hub.exe` 和 `codetty-cli.exe`。

也可以直接使用 Go 构建：

```bash
go build -o codetty-hub ./cmd/codetty-hub
go build -o codetty-cli ./cmd/codetty-cli

# Windows
go build -o codetty-hub.exe ./cmd/codetty-hub
go build -o codetty-cli.exe ./cmd/codetty-cli
```

安装到 `/usr/local/bin`：

```bash
./build.sh install
```

### 使用

CodeTTY 需要 mTLS 证书。详见下面的 **安全配置** 部分。快速示例：

```bash
# 生成证书（第一次）
codetty-hub init-ca --out ~/.codetty/certs
codetty-hub issue-cert --name alice --out ~/.codetty/certs

# 启动 hub（监控机）
codetty-hub --addr :8080 \
  --cert ~/.codetty/certs/server.crt \
  --key ~/.codetty/certs/server.key \
  --ca ~/.codetty/certs/ca.crt

# 远程机器上启动 cli（使用证书）
codetty-cli --server wss://hub-ip:8080/ws \
  --cert-dir ~/.codetty/certs \
  -- bash
```

更多选项和配置详见 **安全配置** 部分。

### 安全配置

CodeTTY 默认使用互相TLS（mTLS）。必须生成证书和密钥文件，或明确使用 `--insecure` 标志禁用。

#### 1. 初始化证书颁发机构（CA）

首次使用时，在监控机上初始化 CA：

```bash
codetty-hub init-ca --out ~/.codetty/certs
```

此命令生成：
- `ca.crt` — CA 证书（用于验证客户端）
- `ca.key` — CA 私钥（用于签署客户端证书）
- `server.crt` — 服务器证书（由 CA 签署）
- `server.key` — 服务器私钥

#### 2. 为客户端签发证书

为每个 CLI 客户端（远程机器）签发证书：

```bash
codetty-hub issue-cert --name alice --out ~/.codetty/certs
codetty-hub issue-cert --name bob --out ~/.codetty/certs
```

每条命令生成 `<name>.crt` 和 `<name>.key` 文件。

#### 3. 启动 Hub（服务端）

使用证书启动 hub：

```bash
codetty-hub --addr :8080 \
  --cert ~/.codetty/certs/server.crt \
  --key ~/.codetty/certs/server.key \
  --ca ~/.codetty/certs/ca.crt
```

#### 4. 启动 CLI（客户端）

在远程机器上使用客户端证书启动 cli：

```bash
# 指定证书文件
codetty-cli --server wss://hub-ip:8080/ws \
  --cert ~/.codetty/alice.crt \
  --key ~/.codetty/alice.key \
  --ca ~/.codetty/ca.crt \
  -- bash

# 或使用 --cert-dir 查找 client.crt, client.key, ca.crt
codetty-cli --server wss://hub-ip:8080/ws \
  --cert-dir ~/.codetty/certs \
  -- bash
```

#### 证书管理命令

**列出证书：**

```bash
codetty-hub list-certs --ca-dir ~/.codetty/certs
```

**撤销证书：**

```bash
codetty-hub revoke-cert --name alice --ca-dir ~/.codetty/certs
```

#### 不安全模式（开发用）

**警告：** 不安全模式禁用加密和身份认证，仅应在开发或受信任的隔离网络中使用。

启动 hub（不安全）：

```bash
codetty-hub --insecure --addr :8080
```

启动 CLI（不安全）：

```bash
codetty-cli --insecure --server ws://hub-ip:8080/ws -- bash
```

### TUI 快捷键

| 按键 | 功能 |
|------|------|
| Tab / → / l | 下一个会话 |
| Shift+Tab / ← / h | 上一个会话 |
| Enter / f | 全屏选中会话 |
| ESC | 退出全屏 |
| 鼠标点击 | 选择会话 |
| q / Ctrl+C | 退出 |

## 项目结构

```
codetty/
├── cmd/
│   ├── codetty-hub/    # Hub 服务端入口
│   └── codetty-cli/    # CLI 客户端入口
├── hub/                # Hub 核心逻辑、TUI、布局
├── protocol/           # WebSocket 二进制协议定义
├── build.sh            # 构建脚本
├── build.bat           # Windows 构建脚本
├── go.mod
└── go.sum
```

## 第三方依赖

| 依赖 | 用途 |
|------|------|
| [gorilla/websocket](https://github.com/gorilla/websocket) | WebSocket 通信 |
| [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI 框架 (Elm 架构) |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | TUI 样式与布局 |
| [charmbracelet/x/ansi](https://github.com/charmbracelet/x) | ANSI 转义序列处理 |
| [creack/pty](https://github.com/creack/pty) | Unix PTY 创建与管理 |
| [UserExistsError/conpty](https://github.com/UserExistsError/conpty) | Windows ConPTY 创建与管理 |
| [hinshun/vt10x](https://github.com/hinshun/vt10x) | VT100 终端仿真 (屏幕快照) |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | 终端状态管理 |

## 协议

基于 WebSocket Binary 帧的单字节类型前缀协议：

| 方向 | 类型字节 | 含义 |
|------|---------|------|
| cli → hub | `r` | 注册 (JSON payload，使用 mTLS 时包含用户身份) |
| cli → hub | `o` | PTY 输出数据 |
| cli → hub | `R` | 屏幕重放快照 |
| hub → cli | `i` | 键盘输入 |
| hub → cli | `s` | 调整终端大小 (JSON payload) |

## 系统要求

- Go 1.25+
- Linux / macOS / Windows
- Windows CLI 需要 Windows 10 version 1809+ 的 ConPTY；建议使用 Windows Terminal
