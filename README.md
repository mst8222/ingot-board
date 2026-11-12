<div align="center">

# Ingot Board

**人类派活 · AI Agent 领取执行 · 人类验收**

`v1.0.0` · [GitHub 仓库](https://github.com/mst8222/ingot-board) · 自托管 · 单机单实例

面向本机多个 AI Agent 编辑器（opencode、Cursor、WorkBuddy、Trae、Pi Agent、Qoder、Deepseek Harness 等）的任务看板。人类在看板上整理与派发任务，Agent 通过 MCP / Runner / REST 三种通道之一扫描并领取属于自己的任务，完成后提交等待人类评审。

<p>
  <a href="#-项目简介">项目简介</a> •
  <a href="#-功能能力">功能能力</a> •
  <a href="#-快速开始">快速开始</a> •
  <a href="#-手机与聊天软件">手机与聊天软件</a> •
  <a href="#-配置与数据">配置与数据</a> •
  <a href="#-http-api-概览">HTTP API</a> •
  <a href="#-开发与扩展">开发与扩展</a> •
  <a href="#-说明">说明</a>
</p>

</div>

---

## 📝 项目简介

Ingot Board 是一个自托管的本机任务看板：人类负责想清楚要做什么、派发与验收，多个 AI Agent 编辑器负责干活。它解决的问题是——当你同时在 opencode、Cursor、WorkBuddy、Trae 等多个编辑器里养着 Agent 时，任务散落在各个会话里没人管得过来。

看板把任务收敛到一处：人类在看板上整理与派发，Agent 通过统一通道领取、心跳、提交，人类评审通过或驳回（驳回原因会下发给 Agent）。全程事件留痕，SSE 实时推送到看板。

> [!IMPORTANT]
> - 本服务仅监听本机（默认 `127.0.0.1:18721`），手机访问走局域网或 Tailscale Funnel，不把免登录的本机端口暴露到公网。
> - **同一个数据目录只允许运行一个服务实例**（`ingot` 与桌面端也不能同时用同一目录）：两个实例各自的 SSE 推送与待评审通知互不相通。第二个实例启动时会报错并指出已在运行的地址。

---

## ✨ 功能能力

| 领域 | 你能做什么 |
| --- | --- |
| 任务流 | 五状态流转：灵感 → 待执行 → 执行中 → 待评审 → 已完成；非法流转在服务端状态机拒绝 |
| 任务指派 | 任务必须指派给一个已启用的 Agent 才能派发，Agent 只能看到指派给自己的任务 |
| 原子领取 | 数据库事务内条件更新保证同一任务只被领取一次，领取后带租约与心跳 |
| 租约回收 | Agent 假死（心跳超时）后服务端自动把任务回收回待执行，全程留事件痕迹 |
| 人类评审 | 待评审任务可驳回（必填原因）或通过（终态）；驳回原因与人类备注会下发给 Agent（MCP 领取结果、Runner 提示词、Agent 端时间线接口） |
| 项目管理 | 项目实体（名称、颜色、归档）；任务可挂项目，看板按项目服务端筛选；项目可绑定多个本机工作目录，目录可反查所属项目 |
| 设备范围 | 手机配对的设备令牌带显式范围（24 个操作的分组勾选）；可按项目授权——范围内项目之外的带项目任务、范围外目录一律 403 |
| 活动时间线 | 任务全部流转、备注、评审意见 append-only 记录，SSE 实时推送到看板 |
| 智能派发 | 派发时自动分类任务并选一个仍有额度的 Agent，额度用尽自动转交 |
| 两种前端形态 | Web GUI（单二进制内嵌 SPA）；Wails v3 桌面客户端（同一内核、同一前端） |
| 手机控制台 | 同一套页面在手机上改成底栏和主按钮，支持 PWA 与 Web Push 待评审通知；桌面仍执行本机 Agent，手机只下达任务、查看进度和结果 |
| 看板交互 | 整卡拖拽流转与列内手动排序；按宽度自适应列数（其余列收为页签，拖拽悬停页签即切换，无横向滚动） |
| 聊天软件 | 待评审出站 Webhook 对接飞书 / 钉钉 / 企业微信 / Slack / Discord 群机器人；Telegram 参考适配器可双向回复 |

### 任务状态机

```
idea ──人类派发──► todo ──agent 原子领取──► doing ──agent 提交──► review
 ▲                   ▲                                                     │
 │                   ├──────────── 驳回（必填原因）◄──────────────────────┤
 │                   │                                                    │
 └── 收回灵感箱 ◄────┘                                                    │
 │                                                                        │
 └──────────────────── 评审通过 ──► approved（终态）◄──────────────────────┘
```

- `idea` 灵感箱：仅人类可见，任何 Agent 接口都不会返回该状态任务
- `idea → todo`（派发）：任务必须已指派给一个**已启用**的 Agent，否则进了待执行也无人能领
- `doing`：Agent 持有租约，心跳续租；超时未续租由服务端回收为 `todo`；执行中的任务不能归档
- Agent 只能做：领取（todo→doing）、心跳、提交（doing→review）、放弃（doing→todo）、备注、读取自己任务的时间线

---

## 🚀 快速开始

### 构建与启动

依赖：Go 1.26+、Node.js 20+（仅构建前端时需要）。

```powershell
# 构建前端产物（输出到 internal/webassets/dist，被 Go 内嵌）
cd web
npm install
npm run build
cd ..

# 构建 Web 服务单二进制与 CLI
go build -o ingot.exe ./cmd/ingot
go build -o ingot-agent.exe ./cmd/ingot-agent

# 启动服务（默认 127.0.0.1:18721，数据目录 %AppData%/IngotBoard，自动打开浏览器）
.\ingot.exe
```

浏览器访问 `http://127.0.0.1:18721`。服务启动后会把实际地址写入数据目录的 `endpoint.json`，`mcp` / `runner` 据此自动发现服务。

### 桌面客户端（Wails v3）

```powershell
# 构建（前端产物 + 桌面端 exe，输出到 dist/ingot-desktop.exe）
python scripts/build_desktop.py

# 启动：进程内拉起看板服务（随机端口），用系统 WebView2 打开
.\dist\ingot-desktop.exe
```

桌面端不重写任何业务逻辑：同一套前端、同一份 SQLite 数据、同一个 API，只是外面套了一个原生窗口。行为要点：

- 服务监听**随机本地端口**，实际地址写入数据目录的 `endpoint.json`，编辑器里的 MCP 不传 `--url` 即可在每次重启后自动连上
- 系统托盘常驻：双击菜单「显示看板」唤起窗口，「退出」才真正结束进程
- 关闭窗口只是隐藏，看板服务与 SSE 仍在后台运行
- Agent 提交任务进入「待评审」时发系统通知（`--no-notify` 可关）
- 想用前端 dev server 调 UI：`ingot-desktop.exe --dev-url http://localhost:5173`（此时服务固定用 18721，以匹配 `web/vite.config.ts` 里的 /api 代理）

### 登记 Agent 并获取 Token

```powershell
.\ingot-agent.exe agent add --name opencode --kind runner
# 输出访问 Token（仅显示一次，请妥善保存）

.\ingot-agent.exe agent list
.\ingot-agent.exe agent token --id 1     # 轮换 Token（旧 Token 立即失效）
.\ingot-agent.exe agent disable --id 1   # 禁用后不能领取，也不能再被指派/派发
```

所有 `agent` 子命令都支持 `--data-dir` 指定数据目录。也可在 Web 界面「Agent 管理」中完成登记与 Token 管理。

把本机已安装的执行端一次性登记，并写入各自的 MCP 与用户级规则（保留配置里已有的其它服务）：

```powershell
.\ingot-agent.exe connect
```

跑完看报告：能用通道的会启用，本机缺东西的也会登记但置为禁用，并写明缺什么（例如 Pi 缺 `pi-acp`、DeepSeek Harness 还没有可写的 `mcp.json`）。补齐后重跑一次 `connect` 即可启用。

### Agent 接入方式

三种通道共用同一套领取与租约规则，按可自动化程度选择。`mcp` / `runner` 省略 `--url` 时从数据目录的 `endpoint.json` 自动发现服务地址（每次请求前重新读取，看板重启换端口也能连上）。

**通道 A：MCP（Cursor、Trae、Qoder、WorkBuddy 等支持 MCP 的编辑器）**

在编辑器的 MCP 配置中注册 stdio 服务：

```json
{
  "ingot": {
    "command": "F:\\path\\to\\ingot-agent.exe",
    "args": ["mcp", "--token", "igt_xxx"]
  }
}
```

提供八个工具：`list_my_tasks`（按看板顺序，第一条最优先）、`claim_task`（返回任务与人类反馈）、`get_task_feedback`、`heartbeat_task`、`submit_task`、`release_task`、`report_quota`（额度上报）、`add_task_note`。

编辑器不会自己轮询，"何时扫描看板"需要靠编辑器规则驱动 Agent。用 `rules` 子命令生成规则文件：

```powershell
# Cursor：写入项目规则（alwaysApply）
.\ingot-agent.exe rules --format mdc --out .cursor\rules\ingot.mdc

# Trae / Qoder 等：输出通用 Markdown，放进对应编辑器的规则文件
.\ingot-agent.exe rules --format md --out .trae\rules\ingot.md
```

规则要求 Agent 在会话开始和提交任务后扫描看板、一次只领一个、被驳回时逐条处理驳回意见、长任务定期心跳、提交时写清改动/验证/产出物，做不了就带原因退回。

MCP 进程由编辑器拉起并长期挂着，启动时看板还没运行也不会退出（工具调用时返回明确错误），只有 Token 被服务端拒绝才会退出。

**通道 B：Runner sidecar（OpenCode、Pi）**

常驻进程轮询任务，领取后用 ACP 拉起本机 Agent。OpenCode 的进程是 `opencode acp`，Pi 的进程是 `pi-acp`。提示词走会话，不放进命令行。执行成功自动提交评审：

```powershell
.\ingot-agent.exe runner --token igt_xxx --adapter opencode --interval 30s
```

- 按看板列内顺序取第一条待执行任务；被驳回过的任务，驳回原因与人类备注会写进提示词
- 启动时校验适配器可执行文件，找不到直接退出，不会领了任务再失败
- 同一任务连续失败 3 次后暂停自动领取（退回原因里会写明）；人类修改任务或重新派发后自动恢复。暂停状态只保存在 runner 进程内，重启 runner 会重新计数
- 心跳偶发网络失败只记录、下个周期重试；租约被服务端拒绝（已被回收）时发送 ACP `session/cancel` 并终止进程
- runner 退出（Ctrl+C）时把执行中的任务退回，不必等租约超时

Runner 进程存活即代表该 Agent 在线可用。

**通道 C：纯 REST（兜底）**

任何能发 HTTP 的脚本/插件，携带 `Authorization: Bearer <token>` 直接调用 Agent 端 API。

### 总管派发（032）

未指定执行端的任务由总管决策：确定性硬门禁（启用、额度、通道可用、能力标签、余额）加可解释打分（档位 40 / 成本序 20 / 余额余量 20 / 在线 10 / 负载 10）选人；模型只估 `low|mid|high` 成本档位，**不指定 Agent**。选人仍复用 011 的 `routing.Pick`，large→floor 降级与额度耗尽改派照旧。

- 默认 **observe**：把未指派任务拖进「待执行」时只生成建议，任务留在灵感箱；点「采纳并派发」一次完成指派与 `idea→todo`。
- 切到 **auto**：拖入时直接由总管定人并派发。开关在「Agent 管理 → 总管派发」，也可调 `GET/PUT /api/v1/dispatch/settings`。
- 建议 10 分钟有效，采纳前重验执行端状态，状态变了返回 `SUGGESTION_STALE`。
- 人类派活与人类终审不变；Agent 端零新增端点，看得到 idea 与 approved 的仍只有人类。

**数值预算（032-B）**：在「Agent 管理」里给执行端录入剩余积分，`POST /api/v1/agents/{id}/balance`（`balance: null` 清回未知）、`GET /api/v1/agents/{id}/ledger` 查看流水。余额未录入时硬门禁跳过、只提示，不伪造数值；只有显式录入后才校验 `余额 ≥ 预估成本`。Agent 可在 `submit_task` / `release_task`（或 Agent 端 `/submit` / `/release`）可选上报 `cost_used`；人类点「通过」时按上报值、否则按预估成本扣减并记一笔流水。不接各家私有计费 API，数据可能陈旧。

### 通用任务转交（028）

在任务详情抽屉里把「待执行 / 待评审」任务转给另一个执行端。总管只按能力与额度给出排序推荐和理由（`GET /api/v1/tasks/{id}/handoff-candidates`），**最终目标由人选定**；确认后走 `POST /api/v1/tasks/{id}/handoff`，不改状态、只改指派与版本，并写一条记录来源、目标与原因的 `reassign` 事件。人选与推荐第一名不同时只在事件里留痕，不阻止人的决定。`执行中` 任务一律拒绝转交（避免租约持有者与指派人错位），Agent 端没有转交入口。额度耗尽时的自动改派仍按 011 原逻辑，只在额度耗尽时触发。

### 任务模板（029）

把一次已终审的任务沉淀成派发期模板：在「已通过」任务的详情抽屉点「存为模板」，再在顶栏「模板」里查看、实例化或删除。模板只快照标题、描述、验收标准、工作目录、优先级、类别、档位、指派执行端与项目，是**可复用的素材**，不是第二套任务状态机。

- 五条人类端路由：`GET/POST /api/v1/templates`、`PATCH/DELETE /api/v1/templates/{id}`、`POST /api/v1/templates/{id}/instantiate`；Agent 端零新增端点。
- 实例化先建 `idea` 任务，再走既有 `routeIdea` 与 `HumanTransition`，不绕过 `GuardRequiresAssignee`；绑定了启用执行端时直接进「待执行」，未绑定且全局为 observe 时只留灵感箱与总管建议，等人采纳。
- 模板里陈旧的类别 / 档位会被过滤后交给总管重新分类；删除或归档模板不影响已经实例化出来的任务。
- 多步骤模板依赖尚未实现的 `020-task-deps`，本版只有单任务模板。

### 定时计划（031）

顶栏「计划」把模板挂到周期上，服务进程内按 `next_run_at` 扫描。这是**人类预先授权的代派发**，不是 Agent 自建任务：服务关闭期间不执行，重启后每条计划最多补偿一次，不回放错过的历史周期。

- 四条人类端路由：`GET/POST /api/v1/schedules`、`PATCH/DELETE /api/v1/schedules/{id}`；周期支持 `interval_minutes`（固定间隔）或 `daily_at`（每日 `HH:MM`，UTC），两者恰选其一。
- 每条计划单独选 `dispatch_mode`：`inherit` 跟随全局双模、`observe` 只落灵感箱与建议等人采纳、**只有显式选 `auto` 才无人值守定人**。选择写进系统事件，`dispatch_decisions.source` 标 `schedule` 并带 `schedule_id`，便于追溯授权来源。
- 实例化仍走 029 模板与既有 `routeIdea` / `HumanTransition`，不绕过 `GuardRequiresAssignee`；`(schedule_id, next_run_at)` 主键幂等，租约把任务回收为 `todo` 也不会重复派发。
- 计划 CRUD 不进 Agent 端与手机共享白名单；调度器与 `StartReaper` 共用生命周期，单实例内只有一个 goroutine，不新起监听。

### 支持的执行端

下面是 `internal/hosts` 注册表里的全部对接执行端，也是看板「执行端」面板与 `GET /api/v1/hosts` 的同一份数据。新增或调整执行端只改注册表，表格与代码同源。

<!-- hosts:begin -->
| 执行端 | 通道 | 档位 | 成本序 | 接入指引 |
|---|---|---|---|---|
| pi | runner | floor | 10 | Runner 通道：PATH 上需有 pi-acp，用 ingot-agent runner --adapter pi 启动 |
| opencode | runner | small | 15 | Runner 通道：需 opencode-cli.exe，用 ingot-agent runner --adapter opencode 启动 |
| cursor | mcp | large | 40 | MCP 通道：connect 写入 ~/.cursor/mcp.json，并生成 .cursor/rules/ingot.mdc |
| trae | mcp | large | 40 | MCP 通道：connect 写入 %APPDATA%/Trae CN/User/mcp.json |
| qoder | mcp | large | 40 | MCP 通道：connect 写入 %APPDATA%/Qoder/SharedClientCache/mcp.json |
| workbuddy | mcp | large | 40 | MCP 通道：connect 同时写 ~/.workbuddy/mcp.json 与旧版 ~/.codebuddy/mcp.json |
| DeepSeek Harness | mcp | large | 40 | MCP 通道：需先存在 %APPDATA%/DeepSeek Harness/mcp.json，否则登记后禁用 |
| claude | mcp | large | 80 | MCP 通道：connect 写入 ~/.claude.json，规则合并进 ~/.claude/CLAUDE.md |
| codex | mcp | large | 80 | MCP 通道：connect 合并写入 ~/.codex/config.toml 与 ~/.codex/AGENTS.md |
<!-- hosts:end -->

`available` 与 `missing_reason` 每次请求按本机文件、PATH 和已登记状态重算，不做缓存。装好某个编辑器后重跑 `ingot-agent connect`，列表会立刻反映变化。

---

## 📱 手机与聊天软件

### 手机

桌面顶栏的「手机」可以打开局域网，或在本机已登录 Tailscale 后允许远程连接。远程地址是 `https://<节点>.ts.net:<端口>`，由 Funnel 公布；证书绑在 Tailscale 地址上，不把外网流量引进免登录的本机端口。

用桌面上的二维码在手机浏览器打开。HTTPS 来源可以安装为 PWA，并打开「通知」接收待评审。`android/` 是一个 WebView 壳，加载同一个地址；「保持接收」才会在后台挂着事件流，默认关闭。

手机不能登记 Agent、轮换 Token 或选择本机目录。编辑器没开时，任务停在待执行，并写明在等哪一个本机 Agent。

### 设备范围与按项目授权

配对签发的 `atd_` 设备令牌带显式范围（局域网与 Tailscale 是同一设备的两条路由，吊销一次两条都失效）：

- 缺省范围 = 手机人类操作全集（新建、派发、编辑正文、通过、驳回、备注、归档等）
- 桌面「手机」面板生成配对码时可收窄范围：选择可访问的项目、放开项目/目录管理操作，或在高级区按 4 组勾选全部操作
- 按项目授权：手机新建或编辑挂项目的任务时，该项目必须在令牌范围内，且任务 workdir 必须属于范围内各项目绑定的目录集合；不挂项目的任务按缺省范围判定
- 范围签发后不可改；需要调整就重新配对

### 聊天软件

新建、派发、通过和驳回用手机上的 PWA 或 `android/` 里的壳。桌面「手机」面板里的「待评审通知地址」在任务第一次进入待评审时 POST 一次：

```json
{"task_id": 1, "title": "修登录", "status": "review"}
```

飞书、钉钉、企业微信、Slack、Discord 的群机器人填它们的 HTTPS 地址。地址为空则不发。发送失败只记在日志里，任务状态照常变化。本机回环可以用 `http://127.0.0.1` 或 `http://localhost`。

要在 Telegram 里回复通过、驳回、备注或新建，运行独立模块里的参考适配器（不进 `ingot`）：

```powershell
cd examples/telegram
$env:TELEGRAM_BOT_TOKEN="机器人令牌"
$env:INGOT_URL="http://127.0.0.1:18721"
go run .
```

把通知地址设成 `http://127.0.0.1:18731/`。适配器把出站 JSON 转成消息，并把「通过 12」「驳回 12 原因」「备注 12 文本」「新建 标题」发给看板上已有的人类接口。适配器不在本机时，设置 `INGOT_DEVICE_TOKEN` 为 `igt_` 设备令牌，并把 `INGOT_URL` 指到手机监听地址。

---

## ⚙️ 配置与数据

| 配置项 | 说明 |
| --- | --- |
| 默认监听 | `127.0.0.1:18721`（`--addr` 可改；桌面端默认随机端口） |
| 默认数据目录 | `%AppData%\IngotBoard`（`--data-dir` 可改），内含 SQLite 单文件 `data.db` 与运行时的 `endpoint.json` |
| Agent Token | `igt_` 前缀随机 32 字节，服务端仅存 sha256 哈希，签发时明文仅显示一次 |
| 设备令牌 | `atd_` 前缀，带显式操作范围与可选项目授权，签发后范围不可改 |
| 租约 | 单次领取 15 分钟，runner 每 5 分钟心跳；服务端每 30 秒回收一次超时租约 |

从旧版本升级（原 AgentTodoList）：服务或 CLI 第一次运行时会把旧数据目录整体改名到新目录，`data.db` 原样继续使用，**升级不丢数据**。只迁移默认数据目录；`--data-dir` 显式指定时按字面使用。旧 Token（`atk_` 前缀）仍然有效，新签发的才用 `igt_`。

---

## 🌐 HTTP API 概览

全部接口挂在 `/api/v1` 下，响应统一为 `{code, msg, data}` 信封。下表列出主要路由；字段与错误码以 `internal/api/routes.go` 为准。

人类端（仅监听本机，免鉴权）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | 健康检查（含 pid，用于实例发现） |
| GET | `/api/v1/meta` | 状态机、流转守卫与枚举定义 |
| GET | `/api/v1/board` | 看板数据（按列分组 + 真实计数） |
| GET | `/api/v1/tasks` | 任务列表（分页、筛选、排序、项目筛选） |
| POST | `/api/v1/tasks` | 新建任务（默认进入灵感箱） |
| GET/PATCH | `/api/v1/tasks/{id}` | 任务详情 / 编辑（全量覆盖，version 乐观锁） |
| POST | `/api/v1/tasks/{id}/transition` | 人工流转（派发/收回/驳回/通过） |
| POST | `/api/v1/tasks/{id}/move` | 列内重排 |
| POST | `/api/v1/tasks/{id}/notes` | 追加备注 |
| GET | `/api/v1/tasks/{id}/events` | 事件时间线（分页） |
| POST | `/api/v1/tasks/{id}/archive` · `/unarchive` | 归档 / 取消归档（执行中的任务不可归档） |
| GET/POST | `/api/v1/agents` | Agent 列表 / 登记 |
| DELETE | `/api/v1/agents/{id}` | 删除 Agent（有在办任务时需 force） |
| POST | `/api/v1/agents/{id}/token` · `/enabled` · `/quota` | 轮换 Token / 启用禁用 / 额度调整 |
| GET/POST/PATCH/DELETE | `/api/v1/projects…` | 项目 CRUD、归档、删除（有任务引用时 409） |
| GET/POST/DELETE | `/api/v1/projects/{id}/workdirs…` | 项目绑定 / 解绑本机目录 |
| GET | `/api/v1/workdirs` | 本机目录反查所属项目 |
| GET | `/api/v1/stream` | SSE 实时事件流 |
| GET | `/api/v1/phone/status` · `/settings` | 手机访问状态与设置 |
| POST | `/api/v1/phone/pair-codes` | 签发配对码（可带收窄后的设备范围） |
| GET | `/api/v1/phone/devices` · POST `/{id}/revoke` | 已配对设备列表 / 吊销 |
| POST | `/api/v1/devices/pair` | 手机端兑换配对码（手机监听地址） |
| GET/POST | `/api/v1/push/vapid-public-key` · `/push/subscription` | Web Push 待评审通知 |

Agent 端（Bearer Token 鉴权）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/agent/me` | 当前 Agent 身份 |
| GET | `/api/v1/agent/tasks` | 指派给自己的任务（不含灵感箱与已完成） |
| POST | `/api/v1/agent/tasks/{id}/claim` | 原子领取（todo→doing，写租约） |
| POST | `/api/v1/agent/tasks/{id}/heartbeat` | 心跳续租 |
| POST | `/api/v1/agent/tasks/{id}/submit` | 提交评审（doing→review，含成果说明） |
| POST | `/api/v1/agent/tasks/{id}/release` | 放弃执行（doing→todo） |
| POST | `/api/v1/agent/tasks/{id}/notes` | 追加备注 |
| GET | `/api/v1/agent/tasks/{id}/events` | 自己任务的时间线（驳回原因、人类备注） |
| POST | `/api/v1/agent/quota` | 上报额度（ok / exhausted） |

---

## 🧑‍💻 开发与扩展

后端用 Go 标准库路由（方法匹配）、modernc.org/sqlite（纯 Go，WAL 模式）、SSE 与 go:embed。前端用 React 19 + Vite + TypeScript + TanStack Query + zustand + dnd-kit + Tailwind CSS + Radix UI。桌面端用 Wails v3（WebView2 / CGO free），与 Web 端共用同一 Go 服务内核，桌面壳只负责窗口、托盘、系统通知，业务零分叉。

| 位置 | 职责 |
| --- | --- |
| `cmd/ingot/` | Web 服务主程序 |
| `cmd/ingot-agent/` | CLI：agent 管理、mcp、runner、rules、connect |
| `cmd/ingot-desktop/` | 桌面客户端主程序（Wails v3） |
| `internal/domain/` | 实体、状态机与流转守卫（流转合法性集中于此） |
| `internal/store/` | SQLite 仓储、原子 claim、租约回收与列内排序 |
| `internal/app/` | 应用服务（HTTP 与桌面端共用） |
| `internal/api/` | HTTP 路由、Token 中间件、SSE、静态资源 |
| `internal/serve/` | 服务装配（供 Web 与桌面壳复用） |
| `internal/endpoint/` | endpoint.json 读写、实例存活判定与服务发现 |
| `internal/mcpserver/` | MCP stdio server |
| `internal/hosts/` | 执行端声明式注册表：支持列表、通道探测与适配器元数据的单一数据源 |
| `internal/runner/` | 轮询与 Agent 进程适配器 |
| `internal/agentclient/` | Agent 侧 HTTP 客户端 |
| `internal/cli/` | 命令行入口与编辑器规则模板 |
| `internal/desktop/` | Wails v3 桌面壳（窗口 + 托盘 + 系统通知） |
| `internal/webassets/` | go:embed 前端产物 |
| `web/` | React SPA 源码与构建配置 |
| `migrations/` | SQL 迁移 |

本仓库是 **v1.0.0 公开发行版**，只包含可构建、可运行的运行时源码、前端 SPA 源码、Android 壳与展示资产；内部规格、研发文档与测试用例不随仓库发布。发布前已在完整工程上通过 Go 全量测试与前端单元测试（183 项）验证。

重新构建前端产物（`go:embed` 会把它们内嵌进二进制）：

```powershell
cd web
npm install
npm run build
```

只改 Go 代码时无需重新构建前端，仓库中的 `internal/webassets/dist` 已是本版产物。

### 后续计划

| 规格 | 内容 |
| --- | --- |
| 009-delivery | 端到端交付与打包 |
| 016-worktree-review | 工作区与 diff 评审 |
| 018-run-log | 运行记录 |
| 019-workflow-file | 仓库工作流文件 |
| 020-task-deps | 任务依赖 |
| 021-same-attempt | 同一尝试续做 |
| 030-agent-memory | 执行端上下文与记忆累积 |

已完成的 `027-host-registry` 把本机执行端元数据抽成单一数据源：看板「Agent 管理」里的支持列表、`GET /api/v1/hosts`、README 清单与 `connect` 行为都由同一份注册表驱动。

已完成的 `028-task-handoff`、`029-task-template`、`031-scheduled-dispatch` 与 `032-dispatch-governance` 构成总管派发体系：转交由人定、模板沉淀派发期素材、计划按人预先授权重跑、双模与数值预算负责选人和门禁；人类派活与人类终审的边界不变。

---

## 📚 说明

本仓库为 v1.0.0 公开发行版，不含内部规格、研发文档与测试工程。使用问题与建议请通过 GitHub Issues 反馈。
