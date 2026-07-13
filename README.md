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
- **多布局模式** — 列表 (list，默认)、焦点放大 (focus-big)、网格 (grid)、全屏 (fullscreen)、浮动窗口
- **可配置命令前缀** — 默认 Ctrl+A（类似 tmux/screen），可通过配置文件自定义，且能自动避免与 tmux 冲突
- **Tmux 集成** — 发现并加载已有 tmux session，统一管理
- **Session 管理器** — 隐藏/显示/关闭 session，面板按钮操作
- **鼠标支持** — 点击聚焦、Ctrl+点击切换、标题栏拖拽重排、文本选择复制
- **面板拖拽重排** — 拖拽侧面板标题栏调整显示顺序
- **宽字符支持** — 正确显示中文/日文/韩文等 CJK 字符
- **Shift/Ctrl/Alt 方向键** — 正确转发修饰键组合（支持 tmux 面板切换等）
- **跨平台 PTY** — Linux/macOS 使用 Unix PTY，Windows 使用 ConPTY
- **自动 daemon** — 首次运行自动启动后台进程，所有会话退出后自动停止
- **Session 配置文件** — 通过 `.codetty.toml` 定义 session 布局，支持 tmux pane 模板和继承

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

# 从配置文件加载 session 布局
codetty up                          # 查找 .codetty.toml
codetty up -f dev.toml              # 指定配置文件

# 关闭指定会话
codetty kill -s myserver

# 停止 daemon 和所有会话
codetty kill-server
```

### TUI 快捷键（速览）

默认前缀键为 `Ctrl+A`（可在配置文件中自定义，见下方[配置](#配置)章节）：

| 按键 | 功能 |
|------|------|
| `Ctrl+A d` | Detach（退出 TUI，会话继续运行） |
| `Ctrl+A c` | 创建新会话 |
| `Ctrl+A t` | 发现/加载 tmux 会话 |
| `Ctrl+A w` | 打开 Session 管理器 |
| `Ctrl+A n/p` | 下一个 / 上一个会话 |
| `Ctrl+A f` | 全屏 / 浮动窗口切换 |
| `Ctrl+A q` | 关闭浮动窗口 |
| `Ctrl+A s` / `Ctrl+A b` | 切换列表 / 焦点放大模式 |
| `Ctrl+A m` | 打开临时浮动 shell |
| `Ctrl+A 1-9` | 跳转到指定编号会话 |

完整快捷键列表和详细用法见 [docs/manual.md](docs/manual.md)。

## 配置

CodeTTY 支持在 `~/.config/codetty/config.toml` 中放置一个可选的用户配置文件
（TOML 格式）。缺省该文件，或省略其中的字段，CodeTTY 都会回退到合理的默认值。

仓库根目录下的 [config.example.toml](config.example.toml) 提供了一份带注释的
示例，复制到 `~/.config/codetty/config.toml` 后按需修改即可：

```bash
mkdir -p ~/.config/codetty
cp config.example.toml ~/.config/codetty/config.toml
```

目前支持的配置项：

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `keybindings.prefix` | 命令前缀键，格式为 `"ctrl+<字母>"`（如 `"ctrl+a"`、`"ctrl+b"`） | 自动检测（见下） |

未显式设置 `keybindings.prefix` 时，CodeTTY 会自动选择默认前缀键：默认使用
`ctrl+a`；但如果检测到系统安装了 tmux，且其全局前缀键
（`tmux show-options -g prefix`）也是 `ctrl+a`，为避免嵌套使用 tmux 时按键
冲突，会自动改用 `ctrl+b`。

## 项目结构

```
codetty/
├── cmd/codetty/     # 主入口、CLI 命令、TUI 渲染、daemon 管理
├── daemon/          # 后台进程：会话管理、Unix Socket 协议、客户端连接
├── terminal/        # PTY 抽象（Unix/Windows）、ScreenMirror
├── vt10x/           # 本地 fork 的 vt10x 终端仿真（支持宽字符）
├── config/          # 用户配置文件（config.toml）加载
├── docs/            # 文档
├── config.example.toml  # 配置文件示例
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
| [BurntSushi/toml](https://github.com/BurntSushi/toml) | 解析用户配置文件 (config.toml) |

## 系统要求

- Go 1.25+
- Linux / macOS / Windows
- Windows 需要 Windows 10 version 1809+（ConPTY）
