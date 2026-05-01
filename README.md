# CodeTTY

反向连接的终端多路复用监控系统。多台远程机器主动连接中心服务，在 TUI 界面中以网格视图实时展示所有终端会话，支持全屏交互操控。

## 架构

```
┌───────────────────────────────────────┐
│  codetty-hub (TUI 服务端，监控机)       │
│  监听 :8080，接受多个 cli 连接          │
└──────────┬──────────┬─────────────────┘
  WebSocket│ WebSocket│  WebSocket
           ▼          ▼
     ┌──────────┐ ┌──────────┐
     │codetty-cli│ │codetty-cli│  ...
     │ machine1 │ │ machine2 │
     └──────────┘ └──────────┘
```

**数据流：**

```
PTY 输出 → cli → WebSocket → hub → TUI 渲染
键盘输入 ← cli ← WebSocket ← hub ← TUI 捕获
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
- **自定义协议** — 基于 WebSocket 的轻量二进制协议，支持注册、输出、输入、调整大小、屏幕重放

## 快速开始

### 构建

```bash
./build.sh
```

生成两个二进制文件：`codetty-hub` 和 `codetty-cli`。

安装到 `/usr/local/bin`：

```bash
./build.sh install
```

### 使用

```bash
# 启动 hub（监控机）
codetty-hub --addr :8080

# 远程机器上启动 cli
codetty-cli --server ws://hub-ip:8080/ws -- bash
codetty-cli --server ws://hub-ip:8080/ws --id myhost -- claude
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
| [creack/pty](https://github.com/creack/pty) | PTY 创建与管理 |
| [hinshun/vt10x](https://github.com/hinshun/vt10x) | VT100 终端仿真 (屏幕快照) |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | 终端状态管理 |

## 协议

基于 WebSocket Binary 帧的单字节类型前缀协议：

| 方向 | 类型字节 | 含义 |
|------|---------|------|
| cli → hub | `r` | 注册 (JSON payload) |
| cli → hub | `o` | PTY 输出数据 |
| cli → hub | `R` | 屏幕重放快照 |
| hub → cli | `i` | 键盘输入 |
| hub → cli | `s` | 调整终端大小 (JSON payload) |

## 系统要求

- Go 1.25+
- Linux (PTY 依赖)
