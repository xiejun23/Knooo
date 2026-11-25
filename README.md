# Knooo

本地查看和检索 Codex、Claude Code、Pi、WorkBuddy、DeepSeek Harness、Antigravity、Gemini CLI、GitHub Copilot CLI 和 Qwen Code 会话。工作回顾按 Git 项目归组，支持 Agent 筛选。搜索支持原文、会话标题、项目路径、Agent 日志目录和会话文件路径，原文命中可定位到消息。

## 在本机运行

```sh
npm ci
npm start
```

首次拉取或依赖变更后运行 `npm ci`。`npm start` 构建前端并启动完整应用，前端已经构建时可直接运行 `go run .`。

源码放在 WSL 时，在 WSL 终端进入仓库根目录运行这些命令。Ubuntu 与 Windows 的 Agent 会话会汇总到同一个会话页，可按环境筛选。Knooo 通过 WSL 互操作发现当前 Windows 用户，并读取已挂载的 Windows 磁盘。Windows 用户也可直接运行下文的 EXE。

默认地址为 `127.0.0.1:48763`，定义在仓库根目录的 [`config.json`](config.json) 中，并随可执行文件打包。默认端口不可用时，Knooo 自动使用系统分配的本机端口。开发时可用 `go run . -listen 127.0.0.1:49001` 临时指定端口。

用 `go run . -demo` 打开独立的演示工作区。它使用构造数据和内存数据库。用 `-no-open` 关闭自动打开，直接在本机浏览器打开启动程序打印的链接。

正式模式启动后自动发现本机历史。在「会话」里可按 Agent 搜索或批量归档，已归档的会话可以恢复。归档只影响 Knooo 的展示、检索和报告，Agent 的原始日志保留在原处。归档状态、已保存主题和知识条目存放在用户配置目录下的 `Knooo/studio.sqlite`。本地摘录、浏览和搜索可离线运行。

在「本机设置 → 模型连接」可添加 **本地网关**，或直连 OpenAI、Anthropic、Google Gemini、DeepSeek、阿里云百炼、OpenRouter 和 OpenAI 兼容服务。每个连接可选品牌图标，图标用于识别连接。

- **Magpie 示例**使用 `http://127.0.0.1:3425/v1` 和 Chat Completions，已配置端口时自动带入该端口。启动 Magpie 后可读取已启用的模型和路由组。
- **CC Switch 示例**使用 `http://127.0.0.1:15721/v1` 和 Responses。在 CC Switch 开启本地代理，配置 Codex 上游，并按代理实际监听端口和模型 ID 填写。
- **其他网关**填写 HTTP 回环地址，再选择网关支持的 Chat Completions 或 Responses。网关有鉴权时填入 API 密钥。

设置页会列出本机 Magpie、CC Switch 和 Claude Code 配置中可导入的连接。点击「导入」后才会保存到 Knooo。也可以继续使用「添加连接」手动配置。

保存前可读取模型目录。目录不可用时，可手动填写模型 ID 再测试。填写模型后，测试会发送一条固定短消息，可能消耗少量 token。连接信息和默认模型保存在本机。API 密钥加密后随连接保存，重启会自动恢复。Windows 使用当前账户的系统保护。Linux 和 WSL 使用应用数据目录中的 `model-keystore`，备份该文件和数据库相当于备份密钥。

每个连接可设提炼输入预算和输出上限，默认分别为 24,000 和 4,096 token。网关目录提供对应模型的上下文窗口或输出硬上限时，Knooo 会按模型收紧预算。输入按 UTF-8 字节加请求开销保守估算。超出预算时优先保留近期消息，最新单条消息过长时保留其开头和结尾。阅读器会显示估算、发送条数和截取情况，确认后才发送选中的会话内容。请求由本地网关按其配置处理，可能转发到云模型供应商。

### 工作回顾与报告

在「本机设置 → 自动工作回顾」选择一个已保存的默认模型连接并启用。应用运行期间会在后台扫描和提炼会话，从最近的会话开始。Knooo 先按本地规则过滤工具日志、环境上下文和简单确认，再由模型整理每个问题的做法、结果和原文依据。每批结果和消息进度都保存在本机。继续在同一会话聊天时，Knooo 从已保存的位置处理新增消息。已处理的历史如果被改写或截断，Knooo 会重新提炼这条会话。重启后，后台任务会使用该连接已保存的密钥继续运行。

在「工作回顾」可多选项目，点击项目标题即可收起或展开其会话。时间范围按会话最近活动日期筛选，可用快捷选项，也可点击日历图标选择起止日期。

左侧「平台日志」记录最近 1,000 条扫描、模型连接和自动回顾事件，可按模块和级别筛选。回顾失败时点「查看回顾日志」，可以查看具体校验原因、关联会话和计划重试时间。日志保存在本机应用数据库，记录运行信息和会话 ID，不保存会话原文、模型响应或 API 密钥。

在「工作报告」使用日报、周报、月报，或点击旁边的日历选择起止日期。起止日期均包含在内，按浏览器的本地时区计算。项目下拉框按 Windows、WSL 发行版等扫描环境分组，只显示所选期间有会话消息的项目。日报突出具体成果和待办，周报归纳多个主题的进展，月报聚焦阶段成果和可复用的发现。

点击「生成报告」后，Knooo 使用所选期间的工作回顾。默认模型可用时，发送用于综合的回顾摘要和编号，会话原文及原文定位保留在本机。默认模型未配置或综合失败时，Knooo 在本地整理有依据的要点。报告显示会话提炼覆盖率，可展开依据并定位原文，核对模型草稿，也可编辑正文后导出 Markdown。

会话优先显示 Agent 已有的名字。Codex 会读取对应账户的会话索引和已落盘的本地状态，其他 Agent 会读取日志里已有的标题或摘要。没有可用名称时，列表会用首条用户消息生成标题。

「会话 → 扫描来源」列出运行环境、已识别的 Agent 日志目录、读取状态和纳入索引的会话数。会话列表只为当前环境和归档范围内有历史的 Agent 显示筛选按钮，Antigravity CLI 与 App 可分别筛选。项目按运行环境和实际项目路径归组，用户主目录下的 WSL 项目显示为 `Ubuntu · ~/work/project`。点击会话行的环境标签即可展开并复制项目目录、Agent 日志目录和会话文件。Windows 端复制 WSL 路径时保留可访问的 UNC 路径。

### 自动发现的会话目录

下列目录相对于当前环境的用户主目录。运行中 WSL 的每个发行版各自扫描。

| Agent | 会话目录 |
| --- | --- |
| Codex | `.codex/sessions`、`.codex/archived_sessions` |
| Claude Code | `.claude/projects` |
| Pi | `.pi/agent/sessions` |
| WorkBuddy | `.workbuddy/projects`、`.workbuddy-ai/projects`、`$WORKBUDDY_CONFIG_DIR/projects` |
| DeepSeek Harness | `.dsh/sessions` 或 `$DSH_HOME/sessions`，含 `.jsonl.zstd` |
| Antigravity CLI | `.gemini/antigravity-cli/conversations` |
| Antigravity App | `.gemini/antigravity/conversations` |
| Gemini CLI | `.gemini/tmp/<项目>/chats` |
| GitHub Copilot CLI | `.copilot/session-state/<会话>/events.jsonl` |
| Qwen Code | `.qwen/projects/<项目>/chats` |

图标来源见 [Agent 图标](ui/assets/agents/README.md)。

## Windows x64

安装包添加开始菜单入口，独立 EXE 可双击运行。两种方式都使用内置启动配置，自动打开默认浏览器，无需填写端口。Windows 端扫描当前 Windows 用户和各运行中 WSL 发行版的默认用户 `$HOME`。停止的发行版会标记为「未运行，未扫描」，启动对应发行版后可点击「刷新会话」。WSL 端扫描当前发行版与可访问的 Windows 历史，其他发行版需从 Windows 端查看。两端各自保存归档、知识条目和模型连接。详见 [构建与发行](packaging/README.md)。版本历史在应用的「关于」和 [CHANGELOG.md](CHANGELOG.md) 中。

## 项目结构

- `main.go` 和 `desktop_*.go` 负责本地进程启动、嵌入资源和打开工作台。
- `internal/studio/` 提供工作台的 HTTP API，并管理搜索、归档、知识、报告和模型连接。
- `internal/sessions/` 负责发现、读取和解析 Agent 会话。`internal/distill/` 负责模型调用和会话提炼。
- `ui/` 存放前端源码，`packaging/` 和 `.github/workflows/` 管理打包和发布。

## 验证

```sh
npm run build
go test ./...
go vet ./...
```
