<div align="center">
  <img src="ui/assets/eagle.png" width="100" height="100" alt="">
  <h1>Knooo</h1>
  <p><strong>让知识成为一种可以被看见的回声</strong></p>
  <p>为开发者打造的 AI Agent 会话与工作回顾桌面应用</p>
  <p>
    <a href="https://github.com/xiejun23/Knooo/releases/latest/download/Knooo-Setup-windows-x64.exe"><img src="docs/assets/windows-download.svg" width="224" height="44" alt="下载 Windows x64 安装版"></a>
  </p>
  <p><a href="https://github.com/xiejun23/Knooo/releases/latest">查看便携版和其他下载</a></p>
</div>

## 快速开始

1. **安装 Knooo。** 下载并双击 `Knooo-Setup-windows-x64.exe`，按提示完成安装。
2. **打开「会话」。** 从开始菜单启动 Knooo。应用会自动读取本机支持的 Agent 会话。若没看到某段历史，可点「扫描来源」查看读取状态。
3. **搜一段旧对话。** 在「搜索」输入记得的关键词，从结果直接跳到命中的原文。浏览和搜索无需配置模型。

想生成第一份工作回顾，可以在「本机设置 → 模型连接」添加或导入连接，再到「自动工作回顾」选定默认模型并启用。回到「工作回顾」，对一条会话点击「立即提炼」。

## 你可以用 Knooo 做什么

- **找回上下文。** 在 Codex、Claude Code、Antigravity 等 Agent 的会话里搜索关键词，也可在 Windows 查看运行中的 WSL 发行版里的会话。
- **回顾项目进展。** 按项目查看做过哪些事、采用了什么方法、结果如何，并点开原文核对。值得复用的经验可以存入知识库。
- **整理工作报告。** 用已有回顾生成日报、周报、月报或自选日期报告，编辑后导出 Markdown。

会话原文和工作记录保存在本机。使用模型提炼时，所选会话内容会发送给你配置的模型服务。报告使用模型综合时，发送的是回顾摘要。

## 为什么叫 Knooo

一次排查可能花了一下午。几周后再遇到相似的问题，你可能想找回当时试过什么、为什么选了这个方案，以及最后是怎么验证的。Knooo 让这些留在旧对话里的经验能在下一次工作中派上用场。

名字取意于 **Knowledge（知识）**、**Observer（观察者）** 和 **Orbit（轨道）**。从会话里观察工作的经过，把值得复用的知识连同依据留下来，让它在下一次需要时回到手边。

[版本记录](CHANGELOG.md) · [提交反馈](https://github.com/xiejun23/Knooo/issues) · [构建与发行](packaging/README.md)
