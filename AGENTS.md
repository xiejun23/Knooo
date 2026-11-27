# Knooo Agent 工程规范

本仓库遵循企业级 Go + 前端桌面应用工程规范。所有 Agent 在新增、修改或重构代码时必须严格遵守以下目录边界与提交流程。

## 目录归属规范（强制）

| 目录 / 文件 | 职责范围 |
| --- | --- |
| `cmd/knooo/` | 程序入口、默认 `config.json`、Windows/Unix 桌面壳（WebView2、托盘、标题栏）与 `.syso` 资源 |
| `internal/` | Go 后端核心业务逻辑（`sessions/`、`studio/`、`distill/`、`update/`） |
| `ui/src/` | 前端 React / TypeScript 源码、样式与静态图标资源 |
| `ui/`（根目录） | 仅保留前端工程配置：`index.html`、`package.json`、`package-lock.json`、`tsconfig.json` |
| `packaging/` | Windows NSIS 安装脚本、应用图标资源、发版说明与发布脚本 |
| `docs/` | 设计文档、项目说明、架构文档与 README 配图资源 |
| `.github/` | CI 工作流与 Issue / PR 模板 |
| 仓库根目录 | **仅允许**标准根文件：`.editorconfig`、`.gitignore`、`AGENTS.md`、`CHANGELOG.md`、`LICENSE`、`Makefile`、`README.md`、`SECURITY.md`、`VERSION`、`embed.go`、`go.mod`、`go.sum` |

### 禁止事项（防止目录腐化）

1. **严禁在仓库根目录新增**业务 `.go` 源码、测试文件、脚本、前端文件、截图、日志或草稿文档。
2. **严禁在 `ui/` 根目录新增** `.ts` / `.tsx` / `.css` 源码文件，所有前端源码必须放入 `ui/src/`。
3. 临时调试脚本、截图或中间产物**严禁提交到仓库**；本地构建产物统一输出到已忽略的 `build/` 或 `ui/dist/`。
4. 独立官网源码位于本地排除目录 `website/`，不要将其混入桌面应用源码或主仓库提交中。

## 改动后必跑验证

提交前按改动范围执行对应校验：

```bash
make test
# 若改动了 cmd/knooo/ 下 Windows 桌面壳，额外验证交叉编译：
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui" -o /tmp/knooo-check.exe ./cmd/knooo && rm -f /tmp/knooo-check.exe
```

## 提交规范

- 本项目所有 Git commit message 统一使用**英文类型前缀 + 中文描述**，格式为 `<type>: <中文说明>`。
- 常用前缀：`feature:`（新功能）、`bugfix:`（缺陷修复）、`refactor:`（重构）、`docs:`（文档）、`test:`（测试）、`chore:`（构建/配置/杂项）、`style:`（样式/格式调整）、`perf:`（性能优化）。

## 版本发布

Windows 发版前，按 [版本发布说明](packaging/README.md#准备版本日志) 对比上一个版本标签和待发布代码，更新根目录的 `CHANGELOG.md`。应用内的「关于」页和 GitHub Release 共用这份日志。普通提交只需记录代码改动，发版时由 Agent 汇总用户可见的变化。创建或推送版本标签以维护者的发版指令为准。
