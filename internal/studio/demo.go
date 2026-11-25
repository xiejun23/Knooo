package studio

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func (a *app) demoReviews() error {
	reviews := []struct{ title, problem, approach, outcome, status string }{
		{"并发回调按订单去重", "避免同一订单被扣款两次", "扣款和状态更新在同一事务提交", "并发回调测试通过", "verified"},
		{"Webhook 重试可按链路追踪", "让 Webhook 超时后的尝试可追踪", "记录链路标识、响应状态和调度时间", "为重试日志补充了本地验证步骤", "resolved"},
		{"会话筛选支持刷新和历史导航", "重新打开会话列表后恢复筛选", "将时间和 Agent 筛选编码进 URL", "刷新和前进后退均可恢复筛选", "verified"},
		{"会话导入可续读未写完的消息", "正确导入尚未写完的 JSONL 消息", "暂存不完整记录，下次扫描续读", "续读后仍能定位原文", "resolved"},
		{"批量导入失败可定位到文件和行号", "从批量导入结果快速找到失败文件", "为每份文件生成带行号的错误摘要", "失败位置可回查", "resolved"},
		{"连接超时用例存在覆盖缺口", "核实 Webhook 超时测试覆盖了哪些场景", "盘点现有超时用例", "现有用例只覆盖读取超时", "observed"},
		{"游标分页补齐三个阶段的示例", "提供可跟着操作的游标分页文档", "补充请求与响应示例", "文档覆盖首屏、下一页和最后一页", "resolved"},
		{"日志导出还原用户选择的时区", "导出日志时遵守用户选定的时区", "用 UTC 执行查询", "导出内容恢复为所选时区", "resolved"},
		{"跟进队列重连退避", "控制队列断线后的重连间隔", "第一次马上重连，后续逐步延长等待时间", "已确定方案", "open"},
	}
	for index, session := range a.sessions {
		review := reviews[index]
		topic := distill.Topic{Title: review.title, Problem: review.problem, Approach: review.approach, Outcome: review.outcome, Status: review.status, Evidence: []string{session.Messages[0].ID, session.Messages[1].ID}}
		checkpoint := reviewCheckpoint{Consumed: len(session.Messages), Topics: []distill.Topic{topic}}
		if _, err := a.saveReview(context.Background(), session.ID, a.revisions[session.ID], checkpoint, storedReview{}); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) demoLogs(now time.Time) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	entries := []struct {
		offset time.Duration
		event  platformEvent
	}{
		{-18*24*time.Hour + 10*time.Hour + 12*time.Minute, platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源", Message: "发现 6 条会话，0 条扫描提示"}},
		{-18*24*time.Hour + 10*time.Hour + 15*time.Minute, platformEvent{Area: "model", Level: "info", Action: "模型连接测试通过", Message: "已验证默认模型网关响应正常"}},
		{-12*24*time.Hour + 9*time.Hour + 30*time.Minute, platformEvent{Area: "model", Level: "error", Action: "读取模型列表失败", Message: "请求 https://api.example.local/v1/models 超时（15s），请检查代理或本机网关状态"}},
		{-12*24*time.Hour + 9*time.Hour + 34*time.Minute, platformEvent{Area: "model", Level: "info", Action: "读取模型列表", Message: "返回 18 个模型"}},
		{-9*24*time.Hour + 14*time.Hour + 22*time.Minute, platformEvent{Area: "scan", Level: "warning", Action: "扫描会话来源", Message: "发现 7 条会话，1 条扫描提示：WSL 发行版 Debian 当前处于停止状态，已跳过扫描"}},
		{-9*24*time.Hour + 15*time.Hour + 8*time.Minute, platformEvent{Area: "review", Level: "error", Action: "回顾内容校验失败", SessionID: "demo-5", Message: "第 1 个问题：缺少支持做法或结果的 Agent 回复依据，已跳过写入并等待下次会话更新后重试"}},
		{-6*24*time.Hour + 11*time.Hour + 40*time.Minute, platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源", Message: "发现 8 条会话，0 条扫描提示"}},
		{-6*24*time.Hour + 11*time.Hour + 45*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-8", Message: "提炼 1 项工作主题：检查日志导出的时间范围"}},
		{-4*24*time.Hour + 16*time.Hour + 5*time.Minute, platformEvent{Area: "model", Level: "warning", Action: "模型请求触发限流", Message: "上游返回 HTTP 429，将在退避冷却后自动恢复调用", RetryAt: now.Add(-4*24*time.Hour + 16*time.Hour + 8*time.Minute).UTC().Format(time.RFC3339)}},
		{-4*24*time.Hour + 16*time.Hour + 9*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-7", Message: "提炼 1 项工作主题：游标分页补齐三个阶段的示例"}},
		{-3*24*time.Hour + 10*time.Hour + 18*time.Minute, platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源", Message: "发现 9 条会话，0 条扫描提示"}},
		{-3*24*time.Hour + 10*time.Hour + 24*time.Minute, platformEvent{Area: "review", Level: "error", Action: "自动回顾请求失败", SessionID: "demo-4", Message: "模型返回空响应体，已保留当前进度并安排自动重试", RetryAt: now.Add(-3*24*time.Hour + 10*time.Hour + 34*time.Minute).UTC().Format(time.RFC3339)}},
		{-3*24*time.Hour + 10*time.Hour + 35*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-4", Message: "提炼 1 项工作主题：会话导入可续读未写完的消息"}},
		{-2*24*time.Hour + 9*time.Hour + 11*time.Minute, platformEvent{Area: "model", Level: "info", Action: "模型连接测试通过"}},
		{-2*24*time.Hour + 14*time.Hour + 47*time.Minute, platformEvent{Area: "scan", Level: "warning", Action: "扫描会话来源", Message: "发现 9 条会话，1 条扫描提示：检测到 1 条未写完的 JSONL 尾部记录，已暂存等待续读"}},
		{-2*24*time.Hour + 15*time.Hour + 2*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-9", Message: "提炼 1 项工作主题：跟进队列重连退避"}},
		{-1*24*time.Hour + 9*time.Hour + 25*time.Minute, platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源", Message: "发现 9 条会话，0 条扫描提示"}},
		{-1*24*time.Hour + 11*time.Hour + 18*time.Minute, platformEvent{Area: "model", Level: "error", Action: "模型连接配置校验失败", Message: "Base URL 缺少协议头（需要 http:// 或 https://）"}},
		{-1*24*time.Hour + 11*time.Hour + 20*time.Minute, platformEvent{Area: "model", Level: "info", Action: "读取模型列表", Message: "返回 24 个模型"}},
		{-1*24*time.Hour + 16*time.Hour + 42*time.Minute, platformEvent{Area: "review", Level: "warning", Action: "会话上下文超出预算", SessionID: "demo-2", Message: "已自动保留最近 42 条消息并压缩较早工具输出，以控制在输入预算内"}},
		{-1*24*time.Hour + 16*time.Hour + 45*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-2", Message: "提炼 1 项工作主题：Webhook 重试可按链路追踪"}},
		{8*time.Hour + 50*time.Minute, platformEvent{Area: "scan", Level: "info", Action: "初次扫描会话来源", Message: "共发现 9 条会话 · 耗时 142ms\nAgent 分布：Claude Code 3 条 · Codex 2 条 · Pi 1 条 · Gemini CLI 1 条 · GitHub Copilot CLI 1 条 · Qwen Code 1 条\n扫描环境：Windows（已扫描）、WSL Ubuntu（已扫描）、WSL Debian（已停止） · 检查 6 个来源目录（6 个有会话）"}},
		{9*time.Hour + 12*time.Minute, platformEvent{Area: "model", Level: "info", Action: "模型连接测试通过", Message: "连接：DeepSeek · 协议：OpenAI 兼容 Chat Completions · 模型：deepseek-chat\n地址：https://api.deepseek.com · 密钥：已配置 · 预算：输入 16000 / 输出 2000\n结果：目标模型响应正常 · 耗时 380ms"}},
		{10*time.Hour + 5*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-3", Message: "提炼 1 项工作主题：会话筛选支持刷新和历史导航"}},
		{11*time.Hour + 30*time.Minute, platformEvent{Area: "scan", Level: "warning", Action: "手动刷新会话来源", Message: "共发现 9 条会话（无会话变更） · 耗时 96ms · 1 条扫描提示\nAgent 分布：Claude Code 3 条 · Codex 2 条 · Pi 1 条 · Gemini CLI 1 条 · GitHub Copilot CLI 1 条 · Qwen Code 1 条\n扫描环境：Windows（已扫描）、WSL Ubuntu（已扫描）、WSL Debian（已停止） · 检查 6 个来源目录（6 个有会话）\n扫描提示（1 条）：\n- WSL 发行版 Debian 未运行，已跳过该发行版目录"}},
		{13*time.Hour + 15*time.Minute, platformEvent{Area: "review", Level: "error", Action: "回顾内容校验失败", SessionID: "demo-1", Message: "第 1 个问题：缺少支持做法或结果的 Agent 回复依据。建议检查对应会话原文或稍后重试。", RetryAt: now.Add(15 * time.Minute).UTC().Format(time.RFC3339)}},
		{13*time.Hour + 42*time.Minute, platformEvent{Area: "model", Level: "info", Action: "读取模型列表", Message: "连接：OpenAI（本地网关） · 协议：本地网关 · Responses\n地址：http://127.0.0.1:15721/v1 · 密钥：未配置（网关可选） · 预算：输入 16000 / 输出 2000\n结果：返回 24 个可用模型 · 耗时 215ms\n模型示例：gpt-5.4、gpt-5.3-codex、claude-sonnet-4-6、gemini-2.5-pro、deepseek-reasoner 等 24 个"}},
		{14*time.Hour + 3*time.Minute, platformEvent{Area: "review", Level: "info", Action: "完成会话自动回顾", SessionID: "demo-1", Message: "提炼 1 项工作主题：并发回调按订单去重"}},
	}
	for i, item := range entries {
		at := today.Add(item.offset)
		if at.After(now) {
			at = now.Add(-time.Duration(len(entries)-i) * time.Minute)
		}
		a.recordEventAt(at, item.event)
	}
	return nil
}

func demoSessions(now time.Time) []sessions.Session {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	rows := []struct {
		project, repo, agent, title, question, answer string
		offset                                        time.Duration
	}{
		{"Atlas API", "atlas-api", "codex", "解决重复请求导致的重复扣款", "支付回调重试时会创建两笔扣款。检查幂等键没有覆盖并发回调的原因。", "并发入口改为共用订单幂等键，写入与状态迁移放到同一事务。支付回调的并发测试已通过。", 14 * time.Hour},
		{"Atlas API", "atlas-api", "claude", "梳理 Webhook 失败重试和观测", "Webhook 超时重试后缺少可追踪的请求号。想把重试原因和 trace ID 放到日志。", "为每次重试记录 trace ID、状态码和下次执行时间，并补充了本地日志验证步骤。", 11 * time.Hour},
		{"Relay Console", "relay-console", "pi", "保留会话列表的筛选条件", "切换到详情再返回时，时间与 Agent 筛选被清空。", "将筛选写入当前页面的查询参数，验证前进、后退与刷新后都能恢复。", 10 * time.Hour},
		{"Relay Console", "relay-console", "codex", "收敛会话导入的异常状态", "Agent 追加记录时可能暂留半行 JSON，该怎么增量扫描？", "在完成换行前保留字节尾部，下一次扫描重新读取那一行并保持消息定位。", 9 * time.Hour},
		{"Papertrail", "papertrail", "claude", "为批量导入补充失败摘要", "批量导入日志太散，怎样快速定位出错的文件？", "按文件汇总失败位置和原因，保留问题行号供使用者回看。", -18 * time.Hour},
		{"Atlas API", "atlas-api", "claude", "子会话 · 检查 Webhook 超时样例", "查一下哪些用例覆盖了连接超时。", "现有用例覆盖读取超时，连接超时需要单独构造。", 11*time.Hour - 5*time.Minute},
		{"Atlas API", "atlas-api", "gemini", "补充 API 分页示例", "给文档增加按游标分页的请求和响应示例。", "示例已覆盖首屏、下一页和最后一页。", 8 * time.Hour},
		{"Papertrail", "papertrail", "copilot", "检查日志导出的时间范围", "导出报表时要保留所选时区。", "将时间范围转换成 UTC 后查询，输出使用用户所选时区。", 7 * time.Hour},
		{"Relay Console", "relay-console", "qwen", "整理连接重试策略", "检查队列连接断开后的重试间隔。", "首次断开后立即重试，此后按退避间隔重试。", 6 * time.Hour},
	}
	result := make([]sessions.Session, 0, len(rows))
	for i, row := range rows {
		t := today.Add(row.offset)
		if t.After(now) {
			t = now.Add(-time.Duration(i) * time.Minute)
		}
		id := fmt.Sprintf("demo-%d", i+1)
		platform, distro := "windows", ""
		path := `C:\Work\` + row.repo
		root := `C:\Users\demo\.pi\agent\sessions`
		if row.agent == "copilot" {
			root = `C:\Users\demo\.copilot\session-state`
		}
		if row.agent != "pi" && row.agent != "copilot" {
			platform, distro = "wsl", "Ubuntu"
			path = `\\wsl.localhost\Ubuntu\home\demo\work\` + row.repo
			root = `\\wsl.localhost\Ubuntu\home\demo\.codex\sessions`
			switch row.agent {
			case "claude":
				root = `\\wsl.localhost\Ubuntu\home\demo\.claude\projects`
			case "gemini":
				root = `\\wsl.localhost\Ubuntu\home\demo\.gemini\tmp`
			case "qwen":
				root = `\\wsl.localhost\Ubuntu\home\demo\.qwen\projects`
			}
		}
		file := root + `\` + id + ".jsonl"
		switch row.agent {
		case "gemini":
			file = root + `\` + row.repo + `\chats\session-` + id + ".jsonl"
		case "copilot":
			file = root + `\` + id + `\events.jsonl`
		case "qwen":
			file = root + `\` + row.repo + `\chats\` + id + ".jsonl"
		}
		result = append(result, sessions.Session{ID: id, Agent: row.agent, Title: row.title, Project: row.project, ProjectPath: path, ProjectDisplayPath: demoDisplayProject(path, distro), Cwd: path, Platform: platform, WSL: distro, SourceRoot: root, Path: file, Start: t.Add(-20 * time.Minute).Format(time.RFC3339), Updated: t.Format(time.RFC3339), Messages: []sessions.Message{
			{ID: id + "-1", Role: "user", Text: row.question, Line: 1, Timestamp: t.Add(-20 * time.Minute).Format(time.RFC3339)},
			{ID: id + "-2", Role: "assistant", Text: row.answer, Line: 2, Timestamp: t.Format(time.RFC3339)},
		}})
	}
	result[5].ParentID = "demo-2"
	return result
}

func demoDisplayProject(path, distro string) string {
	if distro != "" {
		return strings.ReplaceAll(strings.TrimPrefix(path, `\\wsl.localhost\`+distro), `\`, "/")
	}
	return ""
}

func demoCoverage(records []sessions.Session) sessions.ScanReport {
	report := sessions.ScanReport{Environments: []sessions.Environment{
		{Platform: "windows", Home: `C:\Users\demo`, State: "scanned"},
		{Platform: "wsl", WSL: "Ubuntu", Home: `\\wsl.localhost\Ubuntu\home\demo`, State: "scanned"},
		{Platform: "wsl", WSL: "Debian", State: "stopped"},
	}}
	index := map[string]int{}
	for _, s := range records {
		if i, ok := index[s.SourceRoot]; ok {
			report.Roots[i].Sessions++
		} else {
			index[s.SourceRoot] = len(report.Roots)
			report.Roots = append(report.Roots, sessions.ScanRoot{Agent: s.Agent, Platform: s.Platform, WSL: s.WSL, Directory: s.SourceRoot, State: "scanned", Sessions: 1})
		}
	}
	return report
}
