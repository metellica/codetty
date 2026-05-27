# CodeTTY 用户手册

## 目录

- [概述](#概述)
- [安装](#安装)
- [基本概念](#基本概念)
- [命令行用法](#命令行用法)
- [TUI 界面](#tui-界面)
- [快捷键参考](#快捷键参考)
- [鼠标操作](#鼠标操作)
- [布局模式](#布局模式)
- [Session 管理](#session-管理)
- [Tmux 集成](#tmux-集成)
- [浮动窗口](#浮动窗口)
- [文本选择与复制](#文本选择与复制)
- [工作流示例](#工作流示例)
- [故障排除](#故障排除)

---

## 概述

CodeTTY 是一个本地终端多路复用器，提供类似 tmux/screen 的持久会话管理功能。它
由两个组件构成：

1. **Daemon（后台进程）** — 管理多个 PTY 会话，会话在 daemon 中持续运行
2. **TUI（前端界面）** — 基于 Bubble Tea 的交互式终端界面

特点：

- 关闭 TUI 后会话继续运行（detach/attach 模式）
- SSH 断开后重连即可恢复工作现场
- 支持同时管理多个终端会话
- 可加载已有 tmux 会话统一管理
- 鼠标拖拽重排面板、按钮操作

---

## 安装

### 从源码构建

```bash
git clone <repo-url> && cd codetty
./build.sh install    # Linux/macOS: 构建并安装到 /usr/local/bin
```

Windows:

```powershell
.\build.bat           # 生成 codetty.exe
```

### 依赖

- Go 1.25+
- Linux/macOS 或 Windows 10 1809+

---

## 基本概念

### 前缀键

CodeTTY 使用 **Ctrl+A** 作为命令前缀键（类似 tmux 的 Ctrl+B）。

操作方式：先按 Ctrl+A 松开，然后按下功能键。例如 `Ctrl+A d` = 按下 Ctrl+A，
松开，按下 d。

如果需要向程序发送字面 Ctrl+A（如 bash 的行首跳转），按两次 `Ctrl+A Ctrl+A`。

### Session（会话）

一个 session 对应一个 PTY 进程（如 shell、vim、htop）。session 由 daemon 管理，
TUI 只是显示和操作的前端。

### 面板（Panel）

TUI 将每个可见 session 显示为一个面板。面板有不同布局方式：大面板、侧面板、
全屏、浮动窗口等。

---

## 命令行用法

### codetty（无参数）

启动 TUI 并连接到 daemon。如果 daemon 未启动则自动启动。如果没有活跃 session
则自动创建一个 shell session。

```bash
codetty
```

### codetty new

创建新 session 并启动 TUI。

```bash
codetty new                     # 创建默认 shell session
codetty new -s myproject        # 指定 session 名称
codetty new -- htop             # 运行指定命令
codetty new -s dev -- vim .     # 指定名称 + 命令
```

### codetty attach (a)

连接到已有 session 打开 TUI。

```bash
codetty attach                  # attach 默认 session
codetty a -s myproject          # attach 指定 session
```

### codetty ls

列出所有活跃 session。

```bash
codetty ls
```

### codetty kill

关闭指定 session。

```bash
codetty kill -s myproject
```

### codetty kill-server

停止 daemon 和所有 session。

```bash
codetty kill-server
```

---

## TUI 界面

启动后进入 TUI 界面，底部状态栏显示当前 session 列表和快捷键提示。

### 状态栏

底部状态栏显示：

- 当前 session 列表（高亮的是焦点 session）
- 基本快捷键提示

### 标题栏

每个面板顶部的标题栏显示 session 名称。侧面板的标题栏右侧有操作按钮：

- `[-]` — 隐藏面板（session 继续运行，仅不显示）
- `[x]` — 关闭 session（会显示确认对话框）

---

## 快捷键参考

所有前缀命令格式为 `Ctrl+A` 松开后按功能键。

### 会话管理

| 按键 | 功能 |
|------|------|
| `Ctrl+A d` | Detach — 退出 TUI，所有 session 继续运行 |
| `Ctrl+A c` | 创建新 session（有 tmux 时弹出类型选择） |
| `Ctrl+A x` | 关闭当前聚焦的 session |
| `Ctrl+A r` | 重命名当前 session |
| `Ctrl+A t` | 打开 Tmux Session 发现器 |
| `Ctrl+A w` | 打开 Session 管理器 |
| `Ctrl+A m` | 创建临时浮动 shell |

### 导航

| 按键 | 功能 |
|------|------|
| `Ctrl+A n` | 切换到下一个 session |
| `Ctrl+A p` | 切换到上一个 session |
| `Ctrl+A 1`-`9` | 跳转到指定编号的 session |
| `Shift+Tab` | 切换到下一个 session（无需前缀） |

### 布局控制

| 按键 | 功能 |
|------|------|
| `Ctrl+A f` | 全屏切换（根据当前模式智能切换） |
| `Ctrl+A q` | 关闭浮动窗口 |
| `Ctrl+A g` | 网格模式 ↔ 焦点放大模式 |
| `Ctrl+A b` | 切换到焦点放大模式 |
| `Ctrl+A a` | 切换到网格模式 |

### 面板重排

| 按键 | 功能 |
|------|------|
| `Ctrl+A h` | 当前 session 向左移动（调整顺序） |
| `Ctrl+A l` | 当前 session 向右移动（调整顺序） |

### 直接按键（无需前缀）

| 按键 | 功能 |
|------|------|
| `Shift+Tab` | 切换到下一个 session |
| `Ctrl+A Ctrl+A` | 向 session 发送字面 Ctrl+A |

---

## 鼠标操作

### 面板点击

| 操作 | 效果 |
|------|------|
| 单击侧面板内容区 | 将键盘输入焦点切换到该面板 |
| Ctrl+单击侧面板 | 将该 session 提升为大面板（交换位置） |
| 单击大面板内容区 | 重置输入焦点到大面板 |

### 标题栏按钮

侧面板标题栏右侧有 `[-]` 和 `[x]` 按钮：

| 按钮 | 效果 |
|------|------|
| `[-]` | 隐藏该面板（session 继续运行，可通过管理器恢复） |
| `[x]` | 关闭该 session（弹出确认对话框） |

### 标题栏拖拽重排

在侧面板标题栏上按住鼠标并拖动到另一个面板，松开后两个面板交换位置。

拖拽过程中目标面板标题栏会显示 `⬇` 指示器。

**区分拖拽和点击：** 鼠标按下后移动超过阈值才进入拖拽模式，单纯点击不会触发
拖拽。

### 文本选择与复制

在大面板（全屏/焦点放大模式下的主面板或浮动窗口）中：

1. 按住鼠标左键拖动选择文本
2. 松开鼠标后选中文本自动复制到系统剪贴板（OSC 52）
3. 选区高亮显示（反色）

**注意：** 侧面板不支持文本选择，仅大面板/浮动窗口支持。

### 滚轮

鼠标滚轮事件转发到当前焦点 session 的 PTY。

---

## 布局模式

### 焦点放大模式 (Focus-Big)

一个 session 占据大部分屏幕（大面板），其余 session 显示在右侧的小面板列表中。

- 大面板接收键盘输入（除非点击了侧面板）
- 侧面板有标题栏操作按钮和拖拽支持
- `Ctrl+A n/p` 或 `Ctrl+A 1-9` 切换大面板显示的 session

### 网格模式 (Grid)

所有 session 以网格形式平铺显示，每个面板大小均等。

- 点击面板设置焦点
- `Ctrl+A f` 打开焦点 session 的浮动窗口

### 全屏模式 (Fullscreen)

当前 session 占满整个终端窗口。

- 所有鼠标事件转发到 PTY（支持 vim、tmux 等鼠标操作）
- `Ctrl+A f` 退回上一个模式

### 浮动窗口 (Floating)

在当前布局上层叠加显示一个 session 的浮动窗口（带边框）。

- 网格模式下 `Ctrl+A f` 打开
- `Ctrl+A f` 再按可切换到全屏
- `Ctrl+A m` 创建临时浮动 shell（关闭浮动窗口时自动 kill）

---

## Session 管理

### Session 管理器 (Ctrl+A w)

打开覆盖层显示所有 session（包括已隐藏的）：

```
┌─── Session Manager ─────────────────────┐
│                                          │
│  ● bash           [visible]              │
│  ● vim            [visible]              │
│  ○ htop           [hidden]               │
│                                          │
│  j/k: 移动  space: 切换  x: 关闭  esc: 退出  │
└──────────────────────────────────────────┘
```

操作：

| 按键 | 功能 |
|------|------|
| `j` / `↓` | 向下移动光标 |
| `k` / `↑` | 向上移动光标 |
| `Space` / `Enter` | 切换显示/隐藏状态 |
| `h` | 隐藏选中 session |
| `s` | 显示选中 session |
| `x` | 关闭选中 session（需确认） |
| `Esc` / `q` | 退出管理器 |

### 隐藏 / 显示 Session

隐藏一个 session 后：

- session 继续在后台运行
- 不显示在面板布局中
- 可通过 Session 管理器 (`Ctrl+A w`) 恢复显示
- 也可通过侧面板 `[-]` 按钮隐藏

### 关闭 Session

关闭（kill）一个 session 会终止其 PTY 进程。操作前会弹出确认对话框：

```
  Kill session "bash"? [y/n]
```

可通过以下方式触发：

- `Ctrl+A x` — 关闭当前聚焦的 session
- 侧面板 `[x]` 按钮点击
- Session 管理器中按 `x`

---

## Tmux 集成

### Tmux Session 发现器 (Ctrl+A t)

如果系统中有 tmux 运行，`Ctrl+A t` 打开发现器：

```
┌─── Tmux Sessions ───────────────────────┐
│                                          │
│  dev         3 windows   (attached)      │
│  server      1 window    (detached)      │
│  monitor     2 windows   (detached)      │
│                                          │
│  enter: 加载  d: detach  esc: 取消       │
└──────────────────────────────────────────┘
```

- **Enter** — 将选中的 tmux session 作为 PTY session 加载到 CodeTTY 中管理
- **d** — 在 tmux 中 detach 该 session
- **Esc** — 取消并关闭发现器

加载后 tmux session 像普通 session 一样显示在面板中，你可以在 CodeTTY 中正常
操作 tmux（键盘和鼠标都正常转发）。

### 创建新会话 (Ctrl+A c)

如果系统有 tmux，按 `Ctrl+A c` 会弹出类型选择：

- **Shell** — 创建普通 shell session
- **Tmux** — 创建新的 tmux session 并加载

如果系统没有 tmux，直接创建 shell session。

---

## 浮动窗口

浮动窗口是覆盖在当前布局上的单个 session 显示窗口。

### 打开浮动窗口

- 网格模式下 `Ctrl+A f` — 以焦点 session 打开浮动窗口
- `Ctrl+A m` — 创建新的临时浮动 shell

### 操作浮动窗口

- `Ctrl+A q` — 关闭浮动窗口（临时 shell 会自动 kill）
- `Ctrl+A f` — 从浮动切换到全屏
- 正常操作：键盘输入转发到浮动窗口的 session

### 临时浮动 Shell

`Ctrl+A m` 创建的临时 shell 有特殊行为：

- 关闭浮动窗口时自动 kill 该 session
- 不会出现在主面板布局中
- 适合临时执行一个命令后返回正常工作

---

## 文本选择与复制

在大面板或浮动窗口中，可以使用鼠标选择和复制文本：

1. **开始选择** — 在面板内容区按住鼠标左键
2. **拖动选择** — 移动鼠标，选中的文本以反色高亮显示
3. **复制** — 松开鼠标，选中文本通过 OSC 52 发送到系统剪贴板
4. **取消** — 点击其他位置取消选区

### OSC 52 剪贴板

CodeTTY 使用 OSC 52 转义序列设置系统剪贴板。大多数现代终端都支持：

- iTerm2 ✓
- Alacritty ✓
- kitty ✓
- Windows Terminal ✓
- tmux（需要设置 `set -g set-clipboard on`）

如果你的终端不支持 OSC 52，复制后剪贴板不会更新。

---

## 工作流示例

### 新项目开发

```bash
# 启动 CodeTTY，自动创建 shell
codetty

# 在 TUI 中：
# Ctrl+A c 创建编辑器 session（输入名称 "vim"，命令 "vim ."）
# Ctrl+A c 创建第二个 shell 用于运行测试
# Ctrl+A n/p 在 session 之间切换
# Ctrl+A f 全屏编辑
# Ctrl+A f 返回多面板
```

### 接管 tmux 会话

```bash
# 启动 CodeTTY（即使没有活跃 session）
codetty

# Ctrl+A t 打开 tmux 发现器
# 选择需要的 tmux session 按 Enter 加载
# 现在可以在 CodeTTY 面板中操作 tmux session
```

### SSH 远程工作

```bash
# 在远程服务器启动
ssh user@server
codetty new -s work -- vim project/

# SSH 断开后重连
ssh user@server
codetty attach -s work   # 恢复之前的 vim 状态
```

### 多任务管理

```bash
codetty

# 创建多个 session 后，使用 Session 管理器整理：
# Ctrl+A w 打开管理器
# 隐藏暂时不需要的 session（space 键切换）
# 需要时再恢复显示

# 用拖拽重排侧面板顺序（鼠标拖标题栏）
# 用 Ctrl+A h/l 键盘重排
```

---

## 故障排除

### Daemon 无法启动

```bash
# 检查是否有残留的 socket 文件
ls /tmp/codetty-*.sock

# 强制停止旧 daemon
codetty kill-server

# 删除残留 socket 后重新启动
rm /tmp/codetty-*.sock
codetty
```

### 键盘快捷键不响应

- 确认已按下 `Ctrl+A` 前缀键后松开，再按功能键
- 检查是否在覆盖层模式（如 Session 管理器或 Tmux 发现器）中——先按 Esc 退出
- 部分操作需要特定模式（如 `Ctrl+A m` 仅在非全屏模式下工作）

### 显示异常

- 如果出现字符错位，可能是终端不支持宽字符或字体问题
- 调整终端窗口大小会触发重新渲染
- 确保终端支持 256 色或 True Color

### 文本复制不工作

- 确认终端支持 OSC 52（见上文兼容列表）
- 在 tmux 内使用时需要 `set -g set-clipboard on`
- 部分终端需要手动开启剪贴板访问权限

### Windows 特殊注意事项

- 需要 Windows 10 version 1809 或更高版本（ConPTY 支持）
- 建议使用 Windows Terminal（完整支持 ANSI、鼠标、OSC 52）
- cmd.exe 和 PowerShell ISE 可能显示不正常

---

## 版本历史

### v1.0.0

初始发布：

- Daemon 架构与持久会话
- 多布局模式（焦点放大、网格、全屏）
- Ctrl+A 命令前缀
- 鼠标点击切换面板
- Tmux session 发现与加载
- 宽字符支持
- 跨平台 PTY

### v1.0.0 后新增

- **面板拖拽重排** — 拖拽侧面板标题栏调整面板顺序
- **面板按钮** — 侧面板 `[-]`（隐藏）和 `[x]`（关闭）按钮
- **Session 管理器** (`Ctrl+A w`) — 隐藏/显示/关闭 session 的覆盖层界面
- **文本选择与复制** — 在大面板中鼠标选择文本，OSC 52 复制到剪贴板
- **临时浮动 shell** (`Ctrl+A m`) — 快速打开临时 shell，关闭时自动清理
- **Tmux 发现器恢复** (`Ctrl+A t`) — 启动时可直接发现加载 tmux session
