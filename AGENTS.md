# Knooo Agent 指南

## 提交规范

- 本项目所有 Git commit message 统一使用**英文类型前缀 + 中文描述**，格式为 `<type>: <中文说明>`。
- 常用前缀：`feature:`（新功能）、`bugfix:`（缺陷修复）、`refactor:`（重构）、`docs:`（文档）、`test:`（测试）、`chore:`（构建/配置/杂项）、`style:`（样式/格式调整）、`perf:`（性能优化）。

## 版本发布

Windows 发版前，按 [版本发布说明](packaging/README.md#准备版本日志) 对比上一个版本标签和待发布代码，更新根目录的 `CHANGELOG.md`。应用内的「关于」页和 GitHub Release 共用这份日志。普通提交只需记录代码改动，发版时由 Agent 汇总用户可见的变化。创建或推送版本标签以维护者的发版指令为准。
