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

- **多布局模式** — 自适应网格 (auto)、固定列网格 (1-4列)、焦点放大 (focus-big) 三种布局，按需切换
- **全屏模式** — 选中会话后进入全屏交互，键盘输入直接转发到远程 PTY；双击 ESC 或 Ctrl+A 退出
- **Ctrl+A 命令前缀** — 类似 tmux/screen 的操作模式，通过 Ctrl+A + 键执行 hub 命令
- **断线重连** — cli 端指数退避自动重连，重连后发送完整屏幕快照（含颜色、光标、终端模式）无缝恢复
- **TLS 证书热加载** — 运行中发送 SIGHUP 信号即可重新加载证书/CA，无需重启 hub
- **优雅关闭** — SIGTERM/SIGINT 触发优雅关闭，等待活跃连接完成后退出；CLI 自动重连下次启动的 hub
- **mTLS 双向认证** — 默认启用证书验证，内置 CA 管理、证书签发、实时撤销 (CRL)
- **屏幕快照** — 基于 vt10x 终端仿真器维护完整屏幕状态（颜色、属性、光标、终端模式），重连时无缝恢复
- **鼠标支持** — 网格视图中可鼠标点击选择会话
- **自动身份识别** — cli 根据工作目录、主机名、PID 自动生成唯一标识，也可通过 `--id` 手动指定
- **焦点事件过滤** — cli 过滤终端焦点事件 (CSI I/O)，避免干扰远程进程
- **慢客户端保护** — hub 端使用带缓冲的非阻塞写入队列，慢速 WebSocket 客户端不会阻塞其他会话
- **自定义协议** — 基于 WebSocket Binary 帧的轻量二进制协议，支持注册、输出、输入、调整大小、屏幕重放
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
codetty-hub init-ca --out ~/.codetty/certs [--days 365]
```

此命令生成：
- `ca.crt` — CA 证书（用于验证客户端）
- `ca.key` — CA 私钥（用于签署客户端证书）
- `server.crt` — 服务器证书（由 CA 签署）
- `server.key` — 服务器私钥

#### 2. 为客户端签发证书

为每个 CLI 客户端（远程机器）签发证书：

```bash
codetty-hub issue-cert --name alice --ca-dir ~/.codetty/certs --out ~/.codetty/certs [--days 365]
codetty-hub issue-cert --name bob --ca-dir ~/.codetty/certs --out ~/.codetty/certs [--days 365]
```

每条命令生成 `<name>.crt` 和 `<name>.key` 文件。

#### 3. 启动 Hub（服务端）

使用证书启动 hub：

```bash
codetty-hub --addr :8080 \
  --cert ~/.codetty/certs/server.crt \
  --key ~/.codetty/certs/server.key \
  --ca ~/.codetty/certs/ca.crt \
  --crl ~/.codetty/certs/crl.pem   # 可选，启用证书撤销检查
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

# 可选：手动指定会话 ID
codetty-cli --id my-session --server wss://hub-ip:8080/ws \
  --cert-dir ~/.codetty/certs \
  -- bash
```

#### 证书管理命令

**列出证书：**

```bash
codetty-hub list-certs --ca-dir ~/.codetty/certs
```

**撤销证书（立即生效，无需重启 hub）：**

```bash
codetty-hub revoke-cert --name alice --ca-dir ~/.codetty/certs
```

撤销后的证书会被写入 `crl.pem`，hub 在每次 TLS 握手时重新读取 CRL，因此撤销立即生效。

#### 证书热加载

运行中的 hub 收到 SIGHUP 信号时会重新加载服务器证书和 CA：

```bash
kill -HUP <hub-pid>
```

这允许在不中断现有连接的情况下轮换证书。加载失败时旧证书继续生效，并在日志中报告错误。

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

CodeTTY 使用 **Ctrl+A** 作为命令前缀键（类似 tmux 的 Ctrl+B）。在网格/焦点视图中，先按 Ctrl+A 再按命令键执行操作：

#### 前缀命令（Ctrl+A + 键）

| 按键 | 功能 |
|------|------|
| `Ctrl+A` | 发送字面 Ctrl+A 到远程会话 |
| `n` / `p` | 下一个 / 上一个会话 |
| `h` / `l` | 左移 / 右移选中会话（调整顺序） |
| `f` | 全屏选中会话 |
| `1` - `4` | 固定网格列数 |
| `a` | 自适应网格布局 (auto) |
| `b` | 焦点放大布局 (focus-big) |
| `g` | 切回网格布局 |
| `\` | 退出 hub |

#### 直接按键（无需前缀）

| 按键 | 功能 |
|------|------|
| Tab / → / l | 下一个会话 |
| Shift+Tab / ← / h | 上一个会话 |
| Enter / f | 全屏选中会话 |
| 鼠标点击 | 选择会话 |
| q / Ctrl+C | 退出 |

#### 全屏模式

| 按键 | 功能 |
|------|------|
| ESC ESC（300ms 内双击） | 退出全屏 |
| Ctrl+A | 退出全屏 |
| 其他所有按键 | 直接转发到远程 PTY |

### 布局模式

- **auto** — 根据终端宽度和会话数量自动计算网格列数（默认）
- **fixed (1-4)** — 固定 1-4 列网格布局
- **focus-big** — 选中会话占据大面板，其余会话以小面板排列在侧边

### 运维操作

#### 优雅关闭

发送 SIGTERM 或 SIGINT（Ctrl+C）到 hub 进程：

```bash
kill <hub-pid>
# 或 kill -TERM <hub-pid>
```

Hub 会停止接受新连接，等待活跃连接完成后退出。CLI 端检测到断连后会自动重连，因此重启 hub 后所有 CLI 会自动恢复连接并 replay 屏幕状态。

#### 证书热加载

```bash
kill -HUP <hub-pid>
```

重新加载 `--cert`、`--key`、`--ca` 指定的文件。新的 TLS 连接使用新证书，已有连接不受影响。

#### 零停机更新流程

1. 部署新版 `codetty-hub` 二进制
2. 停止旧进程：`kill <old-pid>`
3. 启动新进程（使用相同参数）
4. CLI 自动重连并 replay，画面无缝恢复

## 项目结构

```
codetty/
├── cmd/
│   ├── codetty-hub/    # Hub 服务端入口、信号处理、证书管理子命令
│   └── codetty-cli/    # CLI 客户端入口、PTY 管理、重连逻辑
├── hub/                # Hub 核心逻辑、TUI 模型、布局引擎、全屏模式
├── protocol/           # WebSocket 二进制协议编解码
├── certs/              # TLS 证书管理（CA、签发、撤销、热加载）
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
| [hinshun/vt10x](https://github.com/hinshun/vt10x) | VT100 终端仿真 (屏幕快照与 replay) |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | 终端 raw 模式管理 |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | 系统调用 (信号处理等) |

## 协议

基于 WebSocket Binary 帧的单字节类型前缀协议：

| 方向 | 类型字节 | Payload | 含义 |
|------|---------|---------|------|
| cli → hub | `r` | JSON (`id`, `title`, `cols`, `rows`, `user`) | 注册会话 |
| cli → hub | `o` | 原始终端字节 | PTY 输出数据 |
| cli → hub | `R` | 原始终端字节 | 屏幕重放快照 |
| hub → cli | `i` | 原始终端字节 | 键盘输入 |
| hub → cli | `s` | JSON (`cols`, `rows`) | 调整终端大小 |

## 系统要求

- Go 1.25+
- Linux / macOS / Windows
- Windows CLI 需要 Windows 10 version 1809+ 的 ConPTY；建议使用 Windows Terminal
