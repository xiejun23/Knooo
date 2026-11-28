<div align="center">
  <img src="ui/src/assets/eagle.png" width="100" height="100" alt="Knooo">
  <h1>Knooo</h1>
  <p><strong>让知识成为一种可以被看见的回声</strong></p>
  <p>为开发者打造的本地 AI Agent 会话搜索与工作回顾桌面应用</p>
  <p>
    <a href="https://github.com/xiejun23/Knooo/actions/workflows/checks.yml"><img src="https://github.com/xiejun23/Knooo/actions/workflows/checks.yml/badge.svg" alt="Build and test"></a>
    <a href="https://github.com/xiejun23/Knooo/releases/latest"><img src="https://img.shields.io/github/v/release/xiejun23/Knooo" alt="Latest release"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  </p>
  <p>
    <a href="https://github.com/xiejun23/Knooo/releases/latest/download/Knooo-Setup-windows-x64.exe"><img src="docs/assets/windows-download.svg" width="224" height="44" alt="下载 Windows x64 安装版"></a>
  </p>
  <p>
    <a href="https://knooo.xebron.top/">官网下载</a> ·
    <a href="https://github.com/xiejun23/Knooo/releases/latest/download/Knooo-macOS-universal.dmg">下载 macOS 通用版</a> ·
    <a href="https://github.com/xiejun23/Knooo/releases/latest">便携版与其他下载</a> ·
    <a href="CHANGELOG.md">版本记录</a>
  </p>
</div>

## 快速开始

1. **安装 Knooo。** Windows x64 用户运行 `Knooo-Setup-windows-x64.exe`。macOS 13 及以上用户打开 `Knooo-macOS-universal.dmg`，将 Knooo 拖入「应用程序」。Apple 芯片和 Intel Mac 使用同一个文件。
2. **打开「会话」。** 从 Windows 开始菜单或 Mac 的「应用程序」启动 Knooo。应用会自动读取本机支持的 Agent 会话。若没看到某段历史，可点「扫描来源」查看读取状态。
3. **搜一段旧对话。** 在「搜索」输入记得的关键词，从结果直接跳到命中的原文。浏览和搜索无需配置模型。

想生成第一份工作回顾，可以在「本机设置 → 模型连接」添加或导入连接，再到「自动工作回顾」选定默认模型并启用。回到「工作回顾」，对一条会话点击「立即提炼」。

## 你可以用 Knooo 做什么

- **找回上下文。** 在 Codex、Claude Code、Antigravity、Gemini CLI 等 Agent 的会话里搜索关键词，也可在 Windows 查看运行中的 WSL 发行版里的会话。
- **回顾项目进展。** 按项目查看做过哪些事、采用了什么方法、结果如何，并点开原文核对。值得复用的经验可以存入知识库。
- **整理工作报告。** 用已有回顾生成日报、周报、月报或自选日期报告，编辑后导出 Markdown。

会话原文和工作记录保存在本机。使用模型提炼时，所选会话内容会发送给你配置的模型服务。报告使用模型综合时，发送的是回顾摘要。

## 为什么叫 Knooo

一次排查可能花了一下午。几周后再遇到相似的问题，你可能想找回当时试过什么、为什么选了这个方案，以及最后是怎么验证的。Knooo 让这些留在旧对话里的经验能在下一次工作中派上用场。

名字取意于 **Knowledge（知识）**、**Observer（观察者）** 和 **Orbit（轨道）**。从会话里观察工作的经过，把值得复用的知识连同依据留下来，让它在下一次需要时回到手边。

## 源码开发

```bash
make ui-install   # 安装前端依赖
make dev          # 构建前端并启动本机预览
make test         # 运行前端类型检查与全部 Go 测试
make package-win  # 交叉编译 Windows 便携版与安装包
make package-mac  # 在 macOS 构建通用磁盘映像
```

[版本记录](CHANGELOG.md) · [安全策略](SECURITY.md) · [提交反馈](https://github.com/xiejun23/Knooo/issues) · [构建与发行](packaging/README.md)
