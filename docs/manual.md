# CodeTTY 用户手册

## 目录

- [概述](#概述)
- [安装](#安装)
- [基本概念](#基本概念)
- [命令行用法](#命令行用法)
- [Session 配置文件](#session-配置文件)
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
- 文本选择与 OSC 52 剪贴板复制

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

CodeTTY 默认使用 **Ctrl+A** 作为命令前缀键（类似 tmux 的 Ctrl+B），可在
`~/.config/codetty/config.toml` 中通过 `keybindings.prefix` 自定义（详见
[README「配置」章节](../README.md#配置)）。本手册后续内容均以默认的
`Ctrl+A` 为例，如果你配置了其他前缀键，请自行替换。

操作方式：先按 Ctrl+A 松开，然后按下功能键。例如 `Ctrl+A d` = 按下 Ctrl+A，
松开，按下 d。

如果需要向程序发送字面 Ctrl+A（如 bash 的行首跳转），按两次 `Ctrl+A Ctrl+A`。

按下前缀键后状态栏会显示完整的快捷键提示。

### Session（会话）

一个 session 对应一个 PTY 进程（如 shell、vim、htop）。session 由 daemon 管理，
TUI 只是显示和操作的前端。Session 可以显示、隐藏、重命名或关闭。

Session 内可以有多个 tab。当一个 tab 的进程退出时：

- 非 shell 命令（如 `htop`、`vim`）退出 → 原地重启默认 shell，面板保持可用
- shell（如 `bash`、`zsh`）退出 → 该 tab 自动关闭，切换到剩余 tab
- 最后一个 tab 的 shell 退出 → 整个 session 结束

### 面板（Panel）

TUI 将每个可见 session 显示为一个面板。面板有不同布局方式：大面板、侧面板、
网格、全屏、浮动窗口等。

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

未知命令参数会自动视为 `new` 子命令，例如 `codetty vim .` 等价于 `codetty new -- vim .`。

### codetty attach (a)

连接到已有 session 打开 TUI。

```bash
codetty attach                  # attach 默认 session
codetty a -s myproject          # attach 指定 session
```

### codetty up

从配置文件加载 session 布局，增量创建缺失的 session，然后打开 TUI。
详见 [Session 配置文件](#session-配置文件) 章节。

```bash
codetty up                      # 自动查找 .codetty.toml
codetty up -f dev.toml          # 指定配置文件
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

## Session 配置文件

CodeTTY 支持通过 TOML 配置文件定义 session 布局，包括 native（原生 PTY）session
和 tmux session（含多 window 支持）。使用 `codetty up` 命令加载配置文件并一次性
创建所有 session。

### 使用方法

```bash
codetty up                      # 自动查找配置文件
codetty up -f myconfig.toml     # 指定配置文件
```

配置文件查找顺序：
1. `-f` 指定的路径
2. 当前目录下的 `.codetty.toml`
3. `~/.config/codetty/sessions.toml`

### Delta 行为

`codetty up` 采用增量创建：daemon 中已有同名 session 时跳过创建。这意味着可以
反复运行 `codetty up`，只有缺失的 session 会被创建。

### 基本示例

```toml
layout = "list"               # 初始布局：list / focus-big / fullscreen
focus = "editor"              # 初始聚焦的 session 名称

# 原生 session（直接运行命令）
[[session]]
name = "editor"
command = ["nvim", "."]

[[session]]
name = "server"
command = ["npm", "run", "dev"]
dir = "./backend"             # 工作目录（相对于配置文件所在目录）

[[session]]
name = "shell"                # command 省略则使用默认 shell
hidden = true                 # 启动时隐藏（可通过 Ctrl+A w 显示）
```

### tmux Session 与 Window

配置文件支持定义 tmux session 并在其中创建多个 window（每个 window 运行一个命令）：

```toml
[[session]]
name = "monitoring"
type = "tmux"
tmux_session = "mon"                # tmux session 名称

  [[session.window]]
  name = "logs"
  command = "tail -f /var/log/app.log"

  [[session.window]]
  name = "htop"
  command = "htop"

# 仅 attach 已有 tmux session（不创建 window）
[[session]]
name = "remote"
type = "tmux"
tmux_session = "remote-server"
tmux_attach_only = true
```

tmux window 创建流程：
1. 如果 tmux session 不存在 → 创建 session + 所有 window
2. 如果 tmux session 已存在 → 跳过 window 创建，直接 attach
3. 创建 CodeTTY daemon session 包裹 `tmux attach-session`

### 模板 (Template)

可定义可复用的 window 模板，多个 session 共享同一套 window 配置：

```toml
[template.dev]

  [[template.dev.window]]
  name = "copilot"
  command = "copilot --allow-all"

  [[template.dev.window]]
  name = "git"
  command = "lazygit"

  [[template.dev.window]]
  name = "code"
  command = "nvim ."

  [[template.dev.window]]
  name = "shell"

# 使用模板
[[session]]
name = "frontend"
type = "tmux"
tmux_session = "fe"
template = "dev"
dir = "./frontend"            # dir 应用到所有 window
```

### Session 继承与扩展模板

Session 可以继承模板并进行定制——增加、修改、删除 window：

```toml
[[session]]
name = "fullstack"
type = "tmux"
tmux_session = "full"
template = "dev"
remove_windows = ["git"]         # 删除模板中的 window

  # 同名 window → 覆盖模板中的字段
  [[session.window]]
  name = "code"
  command = "nvim ~/projects"

  # 新名称 → 追加 window
  [[session.window]]
  name = "db"
  command = "docker compose up db"
```

合并规则：

| 操作 | 方法 |
|------|------|
| 增加 window | `session.window` 使用新的 `name` → 追加到列表末尾 |
| 修改 window | `session.window` 使用与模板相同的 `name` → 覆盖 `command`/`dir` 字段 |
| 删除 window | `remove_windows = ["window-name"]` |

### 字段参考

**顶层字段：**

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `layout` | string | `"list"` | 初始布局模式（list / focus-big / fullscreen） |
| `focus` | string | 第一个 session | 初始聚焦 session |

**template.\<name\>：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `window[]` | array | Window 定义列表 |

**session[]：**

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | **必填** | Session 名称（唯一） |
| `type` | string | `"native"` | `"native"` 或 `"tmux"` |
| `command` | string[] | 默认 shell | 原生 session 命令 |
| `dir` | string | 配置文件目录 | 工作目录 |
| `env` | map | `{}` | 额外环境变量 |
| `hidden` | bool | `false` | 启动时隐藏 |
| `template` | string | — | 模板名称（tmux 专用） |
| `tmux_session` | string | — | tmux session 名称（tmux 必填） |
| `tmux_attach_only` | bool | `false` | 仅 attach 已有 tmux session |
| `remove_windows` | string[] | — | 从模板中删除的 window 名称 |
| `window[]` | array | — | 内联 window 或模板覆盖 |

**window[]：**

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `name` | string | **必填** | Window 名称 |
| `command` | string | 默认 shell | Shell 命令字符串 |
| `dir` | string | session 的 `dir` | 工作目录 |

---

## TUI 界面

启动后进入 TUI 界面，底部状态栏显示当前 session 列表和快捷键提示。

### 无 Session 状态

当没有任何 session 时，屏幕中央显示 `⬡ codetty` 标识和提示信息：

- 正常提示："No sessions. Press Ctrl+A c to create one."
- 自动创建中："No sessions. Creating one..."

此状态下仍然支持所有覆盖层操作（tmux 发现器、创建 session 等），状态栏仅在覆盖层
激活时显示。

### 状态栏

底部状态栏显示：

- 当前焦点 session 名称和位置（如 `[bash] 1/3`）
- 当前布局模式（list / fullscreen）
- 操作提示
- 按下 `Ctrl+A` 前缀键后显示完整快捷键列表

### 标题栏

每个面板顶部的标题栏显示 session 名称和编号。侧面板的标题栏右侧有操作按钮：

- `[-]` — 隐藏面板（session 继续运行，仅不显示）
- `[x]` — 关闭 session（会显示确认对话框）

侧面板标题栏有浅绿色背景，大面板标题栏为普通样式。在窄终端（宽度 < 120）下，
焦点放大模式采用垂直布局，底部显示一个小面板列表。

---

## 快捷键参考

所有前缀命令格式为 `Ctrl+A` 松开后按功能键。按下前缀键后状态栏会显示可用命令
的完整列表。

### 会话管理

| 按键 | 功能 |
|------|------|
| `Ctrl+A d` | Detach — 退出 TUI，所有 session 继续运行 |
| `Ctrl+A c` | 创建新 session（有 tmux 时弹出类型选择器） |
| `Ctrl+A x` | 关闭当前聚焦的 session |
| `Ctrl+A r` | 重命名当前 session（预填当前名称，可编辑后确认） |
| `Ctrl+A t` | 打开 Tmux Session 发现器 |
| `Ctrl+A w` | 打开 Session 管理器 |
| `Ctrl+A m` | 创建临时浮动 shell |

### 导航

| 按键 | 功能 |
|------|------|
| `Ctrl+A n` | 切换到下一个 session |
| `Ctrl+A p` | 切换到上一个 session |
| `Ctrl+A 1`-`9` | 跳转到指定编号的 session |
| `Shift+Tab` | 列表模式下在侧栏项间连续跳转（优先跳到需处理的 agent，越过最后一项回到大面板）；焦点放大模式下在侧面板间循环高亮（Enter 浮动 / Space 提升为大面板）；浮动窗口中关闭浮动并高亮下一面板；全屏模式打开 session 切换器（实时预览） |
| `Enter` | 在高亮面板上打开浮动窗口（网格模式或焦点放大的侧面板） |

### 布局控制

| 按键 | 功能 |
|------|------|
| `Ctrl+A f` | 切换布局：浮动→全屏→上级模式；焦点放大/列表→全屏；全屏→返回 |
| `Ctrl+A q` | 关闭浮动窗口 |
| `Ctrl+A s` | 切换到列表模式（默认） |
| `Ctrl+A b` | 切换到焦点放大模式 |

### 面板重排

| 按键 | 功能 |
|------|------|
| `Ctrl+A h` | 当前 session 向左移动（调整顺序） |
| `Ctrl+A l` | 当前 session 向右移动（调整顺序） |

### 网格模式方向键

| 按键 | 功能 |
|------|------|
| `←` / `→` / `↑` / `↓` | 在网格面板间移动焦点选择 |

### 直接按键（无需前缀）

| 按键 | 功能 |
|------|------|
| `Shift+Tab` | 切换到下一个 session / 循环面板高亮 / 列表模式在列表项间跳转 / 全屏模式打开 session 切换器 |
| `Ctrl+A Ctrl+A` | 向 session 发送字面 Ctrl+A |

---

## 鼠标操作

### 面板点击

| 操作 | 效果 |
|------|------|
| 单击侧面板内容区 | 以该 session 打开浮动窗口 |
| 单击大面板内容区 | 若 session 启用了鼠标报告（tmux、vim、copilot 等），按下/拖动/松开事件实时转发到 PTY；否则开始文本选择 |
| Ctrl+单击侧面板 | 将该 session 提升为大面板（交换位置） |
| 单击网格面板内容区 | 以该 session 打开浮动窗口 |

### 标题栏按钮

侧面板标题栏右侧有 `[-]` 和 `[x]` 按钮。支持在焦点放大模式和网格模式下的
所有非大面板上使用。

| 按钮 | 效果 |
|------|------|
| `[-]` | 隐藏该面板（session 继续运行，可通过管理器恢复） |
| `[x]` | 关闭该 session（弹出确认对话框） |

### 标题栏拖拽重排

在侧面板或网格面板的标题栏上按住鼠标并拖动到另一个面板，松开后两个面板交换位置。

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

鼠标滚轮事件转发到有鼠标报告能力的 session PTY（如 tmux、vim 等）。

---

## 布局模式

### 列表模式 (List，默认)

大面板占据屏幕左侧，右侧是固定宽度（28 列）的 session 列表侧栏。
列表显示每个 session 的名称，并在名称下方逐行列出该 session 中带 agent
的 tab 及其状态（一个 session 可有 0 个或多个 agent）：

- `■ copilot blocked`（红色）— agent 等待用户输入
- `● copilot working`（橙色）— agent 正在工作（●/○ 交替闪烁）
- `✓ copilot done`（绿色）— agent 已完成、尚未查看
- `· copilot idle`（灰色）— agent 空闲

操作方式：

- `Shift+Tab` 在侧栏项间连续跳转：优先跳到需要处理的 agent 行（blocked
  最优先，其次 done），然后依次经过各 session 行，越过最后一项后焦点回到
  大面板（列表聚焦时状态栏显示 `list*`）
- 列表聚焦时：`↑`/`↓` 在 session 行和 agent 行之间逐行移动光标（循环）；
  `Enter` 把光标项切换为大面板，若光标在 agent 行上会同时激活对应 agent
  所在的 tab；`Esc` 或任意其他键返回大面板（不会向 PTY 泄漏输入）
- 鼠标点击 session 行直接切换大面板；点击 agent 行则在切换 session 的
  同时激活对应 agent 所在的 tab；滚轮在侧栏上滚动列表
- 当前选中的 session 以低对比度背景色标识，列表光标为高亮背景
- `Ctrl+A n/p` 或 `Ctrl+A 1-9` 仍可直接切换（保持列表模式）
- 大面板的键盘/鼠标行为与焦点放大模式的大面板一致

### 焦点放大模式 (Focus-Big)

一个 session 占据大部分屏幕（大面板，行为与列表模式的大面板完全一致），
其余 session 显示在右侧的小面板列表中。`Ctrl+A b` 进入。

- 大面板接收键盘输入和鼠标事件（渲染、tab 栏、底部信息行与列表模式一致）
- 侧面板不向 session 转发任何键盘/鼠标输入
- 单击侧面板以该 session 打开浮动窗口（适合临时处理简单任务，主工作区
  不变）；Ctrl+单击把它提升为大面板，并自动激活其优先 tab
  （blocked > done > 当前活动 tab）
- `Shift+Tab` 在侧面板间循环高亮（越过最后一项回到大面板，状态栏显示
  `big*`）；高亮时 `↑`/`↓` 移动高亮，`Enter` 打开浮动窗口，`Space`
  提升为大面板，其他键返回大面板（不会向 PTY 泄漏输入）
- `Ctrl+A n/p`、`Ctrl+A 1-9` 直接切换大面板 session
- 侧面板标题栏显示聚合 agent 状态徽标：session 内没有 agent 时不显示
  任何圆点；有 agent 时显示 `■`（blocked）/ `●○` 闪烁（working）/
  `●`（done）/ `○`（idle），标题栏与边框颜色跟列表模式侧栏一致
  （blocked 红 / working 琥珀 / done 绿）

### 网格模式 (Grid)

所有 session 以网格形式平铺显示，每个面板大小均等。每行最多显示 2 个面板。

- `←` `→` `↑` `↓` 方向键在面板间导航
- 点击面板以该 session 打开浮动窗口
- `Enter` 在选中面板上打开浮动窗口
- 标题栏支持 `[-]`/`[x]` 按钮和拖拽重排
- `Ctrl+A f` 打开焦点 session 的浮动窗口

### 全屏模式 (Fullscreen)

当前 session 占满整个终端窗口。

- 所有鼠标事件转发到 PTY（支持 vim、tmux 等鼠标操作）
- 支持文本选择与复制
- `Ctrl+A f` 退回上一个模式（通过模式栈导航）
- `Shift+Tab` 打开 session 切换器（见下）

### 全屏 Session 切换器 (Shift+Tab)

全屏模式下按 `Shift+Tab` 弹出浮动切换器，罗列除当前 session 外的所有
可见 session，每个 session 以焦点放大模式侧面板的样式展示实时输出预览
（约 10 行），便于根据当前状态选择要切换的 session。

- `↓` / `j` — 向下移动（列不变，末行再向下回到首行）
- `↑` / `k` — 向上移动（列不变，首行再向上回到该列末行）
- `→` / `l` — 向右移动（行不变，行末再向右回到行首）
- `←` / `h` — 向左移动（行不变，行首再向左回到行末）
- `Tab` / `Shift+Tab` — 按顺序高亮下一个面板
- `Enter` — 切换到高亮的 session（保持全屏）
- `1`-`9` — 按编号直接切换
- `Esc` / `q` — 关闭切换器，留在当前 session
- 鼠标左键点击面板 — 直接切换到该 session
- 鼠标点击框外 — 关闭切换器
- 滚轮 — 循环切换高亮面板

### 模式栈导航

`Ctrl+A f` 使用模式栈实现智能导航：

- 网格模式 → 打开浮动窗口
- 焦点放大模式 → 切换到全屏
- 浮动窗口 → 切换到全屏（浮动窗口中的 session）
- 全屏模式（普通 session）→ 弹出模式栈，跳过中间的浮动窗口状态
- 全屏模式（临时 shell）→ 返回上一级浮动窗口
- 临时 shell 在全屏时关闭 → 返回浮动窗口

---

## Session 管理

### 创建 Session (Ctrl+A c)

按下 `Ctrl+A c` 后：

- **系统中有 tmux** — 弹出类型选择器：
  - 按 `c` 创建普通 shell session
  - 按 `t` 创建 tmux session
  - 按 `Esc` 取消
- **系统中没有 tmux** — 直接进入名称输入模式

名称输入模式支持：
- 输入 session 名称后按 `Enter` 确认
- `Esc` 取消创建
- `Backspace` 删除字符

### 重命名 Session (Ctrl+A r)

- 进入重命名模式，**预填当前 session 名称**，可直接编辑
- `Enter` 确认，`Esc` 取消，`Backspace` 删除字符

### 关闭 Session（确认对话框）

关闭 session 前显示确认对话框：

```
┌─ Kill session "bash"? ───────┐
│                               │
│  [y/Enter] confirm  [n/Esc] cancel  │
└───────────────────────────────┘
```

支持 `y`/`Enter` 确认或 `n`/`Esc` 取消。

### Session 管理器 (Ctrl+A w)

打开覆盖层显示所有 session（包括已隐藏的），按类型和状态标识：

```
┌─── ⬡ Session Manager ─────────────────────┐
│                                            │
│  2 visible / 3 total                       │
│  ─────────────────────────────             │
│  ▸ visible  bash                           │
│    visible  vim           [tmux]           │
│    hidden   htop                           │
│  ─────────────────────────────             │
│                                            │
│  ↑↓ nav  space/enter toggle  x kill        │
│  h hide  s show  esc close                 │
└────────────────────────────────────────────┘
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

---

## Tmux 集成

### Tmux Session 发现器 (Ctrl+A t)

如果系统中有 tmux 运行，`Ctrl+A t` 打开发现器：

```
┌─── ⬡ Tmux Sessions ───────────────────────┐
│                                            │
│  2/3 selected                              │
│  ─────────────────────────────             │
│  ▸ ◉  dev                                  │
│    ◉  server                               │
│    ○  monitor                              │
│  ─────────────────────────────             │
│                                            │
│  ↑↓ nav  space toggle  a/n all/none        │
│  enter confirm  esc cancel                 │
└────────────────────────────────────────────┘
```

- **Space** — 切换单个 session 的选中状态
- **Enter** — 确认选择：选中且未管理的 tmux session 被加载，取消选中的已管理 session 被卸除
- **a** — 全选所有 tmux session
- **n** — 取消全选
- **j/k / ↑/↓** — 导航
- **Esc** — 取消并关闭发现器

加载后 tmux session 在面板中显示类型标签 `[tmux]`，键盘和鼠标事件正常转发。

### 创建新会话 (Ctrl+A c)

如果系统有 tmux，按 `Ctrl+A c` 会弹出类型选择：

- **Shell** (`c`) — 创建普通 shell session
- **Tmux** (`t`) — 创建新的 tmux session 并加载

如果系统没有 tmux，直接进入 shell session 名称输入。

---

## 浮动窗口

浮动窗口是覆盖在当前布局上的单个 session 显示窗口，尺寸为终端的 80%。
外观与大面板一致：没有顶部标题栏，底部信息行左侧显示 tab 栏（多 tab 时），
右侧显示 `[n] 名称  (^A q close)`；点击信息行上的 tab 可切换 tab。

### 打开浮动窗口

- 网格模式下 `Ctrl+A f` — 以焦点 session 打开浮动窗口
- 焦点放大模式下点击侧面板内容区 — 以该 session 打开浮动窗口
- 网格模式下点击面板 — 以该 session 打开浮动窗口
- 高亮侧面板上按 `Enter` — 以该 session 打开浮动窗口
- `Ctrl+A m` — 创建新的临时浮动 shell

### 操作浮动窗口

- `Ctrl+A q` — 关闭浮动窗口（临时 shell 会自动 kill）
- `Shift+Tab` — 关闭浮动窗口并将焦点移到下一个小面板
- `Ctrl+A f` — 从浮动切换到全屏
- 键盘输入正常转发到浮动窗口的 session
- 鼠标操作：支持文本选择和点击转发

### 临时浮动 Shell

`Ctrl+A m` 创建的临时 shell 有特殊行为：

- 关闭浮动窗口时自动 kill 该 session
- 不会出现在主面板布局中
- 适合临时执行一个命令后返回正常工作

---

## 文本选择与复制

在大面板、全屏模式或浮动窗口中，可以使用鼠标选择和复制文本：

1. **开始选择** — 在面板内容区按住鼠标左键
2. **拖动选择** — 移动鼠标，选中的文本以反色高亮显示
3. **复制** — 松开鼠标，选中文本通过 OSC 52 发送到系统剪贴板
4. **取消** — 点击其他位置取消选区

如果 session 启用了鼠标报告（如 tmux、vim、copilot），鼠标按下、拖动和松开
事件会实时转发到 PTY 程序而非触发文本选择，应用内的点击和拖拽都能正常工作。

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
- 在浮动窗口中，`Ctrl+A` 前缀键仍然有效

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

- Daemon 架构与持久会话管理（detach/attach）
- 四布局模式：列表（List，默认）、焦点放大（Focus-Big）、网格（Grid）、全屏（Fullscreen）
- 窄终端垂直布局自适应
- Ctrl+A 命令前缀系统
- Shift+Tab 面板循环切换
- Enter 打开浮动窗口
- 覆盖层式 Session 管理器（`Ctrl+A w`）
- Tmux Session 发现器（`Ctrl+A t`）——支持选择加载/卸除
- 创建 session 类型选择器（普通 shell / tmux）
- Session 重命名（预填当前名称）
- 临时浮动 shell（`Ctrl+A m`，关闭时自动清理）
- 面板拖拽重排
- 侧面板 `[-]`（隐藏）和 `[x]`（关闭）标题栏按钮
- 鼠标点击切换面板、Ctrl+click 提升大面板
- 文本选择与 OSC 52 剪贴板复制（大面板、全屏、浮动窗口）
- 模式栈导航（`Ctrl+A f` 智能回溯）
- 宽字符支持
- 跨平台 PTY（Linux/macOS/Windows）
