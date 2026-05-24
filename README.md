# CodeTTY

本地终端多路复用器，类似 tmux/screen。后台 daemon 管理 PTY 会话，TUI 前端提供多面板交互。断开 SSH 或关闭终端后会话继续运行，重新 attach 即可恢复。

## 架构

```
┌─────────────────────────────────────────────┐
│            codetty (TUI 前端)                 │
│   Bubble Tea 渲染 · 多布局 · 鼠标/键盘交互    │
└──────────────────┬──────────────────────────┘
                   │ Unix Socket
┌──────────────────▼──────────────────────────┐
│         codetty daemon (后台进程)              │
│  管理多个 PTY session · 自动启动/退出          │
│  ┌─────┐ ┌─────┐ ┌─────┐                    │
│  │ PTY │ │ PTY │ │ PTY │  ...               │
│  │bash │ │ vim │ │htop │                    │
│  └─────┘ └─────┘ └─────┘                    │
└─────────────────────────────────────────────┘
```

## 功能

- **Daemon 架构** — 会话在后台持续运行，detach/attach 不影响进程，SSH 断开后可恢复
- **多布局模式** — 自适应网格 (auto)、焦点放大 (focus-big)、全屏 (fullscreen)
- **Ctrl+A 命令前缀** — 类似 tmux/screen 的操作模式
- **鼠标支持** — 点击选择面板聚焦，Ctrl+点击切换大面板，滚轮转发到 PTY
- **宽字符支持** — 正确显示中文/日文/韩文等 CJK 字符
- **Shift/Ctrl/Alt 方向键** — 正确转发修饰键组合（支持 tmux 面板切换等）
- **跨平台 PTY** — Linux/macOS 使用 Unix PTY，Windows 使用 ConPTY
- **自动 daemon** — 首次运行自动启动后台进程，所有会话退出后自动停止

## 快速开始

### 构建

```bash
./build.sh          # 构建 codetty 二进制
./build.sh install  # 安装到 /usr/local/bin
```

Windows:

```powershell
.\build.bat         # 构建 codetty.exe
```

或直接使用 Go：

```bash
go build -o codetty ./cmd/codetty
```

### 使用

```bash
# 启动（自动创建 shell 会话并打开 TUI）
codetty

# 创建指定命令的会话
codetty new -- htop
codetty new -s myserver -- ssh user@host

# 列出活跃会话
codetty ls

# 重新连接到已有会话
codetty attach
codetty a -s myserver

# 关闭指定会话
codetty kill -s myserver

# 停止 daemon 和所有会话
codetty kill-server
```

### TUI 快捷键

CodeTTY 使用 **Ctrl+A** 作为命令前缀键。

#### 前缀命令（Ctrl+A + 键）

| 按键 | 功能 |
|------|------|
| `d` | Detach（退出 TUI，会话继续运行） |
| `x` | 关闭当前聚焦的会话 |
| `c` | 创建新会话 |
| `r` | 重命名当前会话 |
| `n` / `p` | 下一个 / 上一个会话 |
| `h` / `l` | 左移 / 右移会话顺序 |
| `f` | 切换全屏模式 |
| `g` | 切换网格布局 |
| `b` | 焦点放大布局 (focus-big) |
| `a` | 自适应布局 (auto) |
| `1`-`9` | 跳转到指定编号的会话 |
| `\` | 退出（关闭 TUI） |
| `Ctrl+A` | 发送字面 Ctrl+A 到会话 |

#### 直接按键

| 按键 | 功能 |
|------|------|
| Shift+Tab | 切换到下一个会话 |
| 鼠标单击 | 聚焦到点击的面板 |
| Ctrl+鼠标单击 | 将点击的会话提升为大面板（focus-big 模式） |
| 鼠标滚轮 | 转发到当前聚焦会话 |

#### 布局模式

- **split** — 多面板平铺（默认，2+ 会话时自动使用）
- **focus-big** — 一个大面板 + 侧边小面板列表
- **fullscreen** — 当前会话占满整个终端

## 项目结构

```
codetty/
├── cmd/codetty/     # 主入口、CLI 命令、TUI 渲染、daemon 管理
├── daemon/          # 后台进程：会话管理、Unix Socket 协议、客户端连接
├── terminal/        # PTY 抽象（Unix/Windows）、ScreenMirror
├── vt10x/           # 本地 fork 的 vt10x 终端仿真（支持宽字符）
├── build.sh         # Linux/macOS 构建脚本
├── build.bat        # Windows 构建脚本
├── go.mod
└── go.sum
```

## 第三方依赖

| 依赖 | 用途 |
|------|------|
| [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI 框架 (Elm 架构) |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | TUI 样式与布局 |
| [charmbracelet/x/ansi](https://github.com/charmbracelet/x) | ANSI 转义序列处理 |
| [creack/pty](https://github.com/creack/pty) | Unix PTY 创建与管理 |
| [UserExistsError/conpty](https://github.com/UserExistsError/conpty) | Windows ConPTY |
| [mattn/go-runewidth](https://github.com/mattn/go-runewidth) | Unicode 字符宽度计算 |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | 终端 raw 模式管理 |

## 系统要求

- Go 1.25+
- Linux / macOS / Windows
- Windows 需要 Windows 10 version 1809+（ConPTY）

