package studio

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

const (
	defaultLogPageSize = 20
	maxLogPageSize     = 200
)

type platformEvent struct {
	ID        int64  `json:"id"`
	Time      string `json:"time"`
	Area      string `json:"area"`
	Level     string `json:"level"`
	Action    string `json:"action"`
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message,omitempty"`
	RetryAt   string `json:"retryAt,omitempty"`
}

type platformLogCounts struct {
	All    int            `json:"all"`
	Levels map[string]int `json:"levels"`
	Areas  map[string]int `json:"areas"`
}

type platformLogsResponse struct {
	Events   []platformEvent   `json:"events"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
	Counts   platformLogCounts `json:"counts"`
}

func (a *app) recordEvent(event platformEvent) {
	a.recordEventAt(time.Now(), event)
}

func (a *app) recordEventAt(at time.Time, event platformEvent) {
	_, err := a.db.Exec(`INSERT INTO platform_logs (time,area,level,action,session,message,retry_at)
 VALUES (?,?,?,?,?,?,?)`, at.UTC().Format("2006-01-02T15:04:05.000000000Z"), event.Area,
		event.Level, event.Action, event.SessionID, event.Message, event.RetryAt)
	if err != nil {
		log.Print("Knooo 无法保存平台日志")
	}
}

func parseLogBound(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", err
	}
	utc := parsed.UTC()
	if utc.Nanosecond() == 0 {
		// Whole-second UTC prefix compares accurately against both legacy RFC3339Nano
		// rows (which omit trailing zeros) and fixed-width nanosecond rows.
		return utc.Format("2006-01-02T15:04:05"), nil
	}
	return utc.Format("2006-01-02T15:04:05.000000000Z"), nil
}

func parsePositiveInt(raw string, fallback int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, false
	}
	return value, true
}

func (a *app) platformLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	area, level := query.Get("area"), query.Get("level")
	switch area {
	case "", "scan", "review", "model":
	default:
		http.Error(w, "无效的日志模块", http.StatusBadRequest)
		return
	}
	switch level {
	case "", "info", "warning", "error":
	default:
		http.Error(w, "无效的日志级别", http.StatusBadRequest)
		return
	}
	fromBound, err := parseLogBound(query.Get("from"))
	if err != nil {
		http.Error(w, "无效的开始时间", http.StatusBadRequest)
		return
	}
	toBound, err := parseLogBound(query.Get("to"))
	if err != nil {
		http.Error(w, "无效的结束时间", http.StatusBadRequest)
		return
	}
	page, ok := parsePositiveInt(query.Get("page"), 1)
	if !ok {
		http.Error(w, "无效的页码", http.StatusBadRequest)
		return
	}
	pageSize, ok := parsePositiveInt(query.Get("pageSize"), defaultLogPageSize)
	if !ok || pageSize > maxLogPageSize {
		http.Error(w, "无效的每页条数", http.StatusBadRequest)
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(query.Get("q")))

	// Common time + keyword predicate.
	const baseWhere = `(?='' OR time>=?) AND (?='' OR time<?)
 AND (?='' OR instr(lower(action),?)>0 OR instr(lower(message),?)>0 OR instr(lower(session),?)>0)`
	baseArgs := []any{fromBound, fromBound, toBound, toBound, keyword, keyword, keyword, keyword}

	counts := platformLogCounts{
		Levels: map[string]int{"error": 0, "warning": 0, "info": 0},
		Areas:  map[string]int{"review": 0, "scan": 0, "model": 0},
	}

	// Count total logs in time + keyword scope.
	if err := a.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM platform_logs WHERE `+baseWhere, baseArgs...).Scan(&counts.All); err != nil {
		http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
		return
	}

	// Level facet counts (respecting area + time + keyword so users can switch levels smoothly).
	levelArgs := append([]any{area, area}, baseArgs...)
	levelRows, err := a.db.QueryContext(r.Context(),
		`SELECT level, COUNT(*) FROM platform_logs WHERE (?='' OR area=?) AND `+baseWhere+` GROUP BY level`,
		levelArgs...)
	if err != nil {
		http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
		return
	}
	for levelRows.Next() {
		var name string
		var n int
		if err := levelRows.Scan(&name, &n); err != nil {
			levelRows.Close()
			http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
			return
		}
		counts.Levels[name] = n
	}
	if err := levelRows.Err(); err != nil {
		levelRows.Close()
		http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
		return
	}
	levelRows.Close()

	// Area facet counts (respecting level + time + keyword so users see which modules match).
	areaArgs := append([]any{level, level}, baseArgs...)
	areaRows, err := a.db.QueryContext(r.Context(),
		`SELECT area, COUNT(*) FROM platform_logs WHERE (?='' OR level=?) AND `+baseWhere+` GROUP BY area`,
		areaArgs...)
	if err != nil {
		http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
		return
	}
	for areaRows.Next() {
		var name string
		var n int
		if err := areaRows.Scan(&name, &n); err != nil {
			areaRows.Close()
			http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
			return
		}
		counts.Areas[name] = n
	}
	if err := areaRows.Err(); err != nil {
		areaRows.Close()
		http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
		return
	}
	areaRows.Close()

	// Exact total for the current full filter (area + level + time + keyword).
	total := 0
	if area == "" && level == "" {
		total = counts.All
	} else if area == "" {
		total = counts.Levels[level]
	} else if level == "" {
		total = counts.Areas[area]
	} else {
		total = counts.Areas[area]
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	events := []platformEvent{}
	if total > 0 {
		queryArgs := append([]any{area, area, level, level}, baseArgs...)
		queryArgs = append(queryArgs, pageSize, offset)
		rows, err := a.db.QueryContext(r.Context(), `SELECT id,time,area,level,action,session,message,retry_at
 FROM platform_logs WHERE (?='' OR area=?) AND (?='' OR level=?) AND `+baseWhere+`
 ORDER BY id DESC LIMIT ? OFFSET ?`, queryArgs...)
		if err != nil {
			http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var event platformEvent
			if err := rows.Scan(&event.ID, &event.Time, &event.Area, &event.Level, &event.Action, &event.SessionID, &event.Message, &event.RetryAt); err != nil {
				http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
				return
			}
			events = append(events, event)
		}
		if rows.Err() != nil {
			http.Error(w, "无法读取平台日志", http.StatusInternalServerError)
			return
		}
	}

	jsonReply(w, platformLogsResponse{
		Events:   events,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Counts:   counts,
	})
}

func safeLogURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	clean := url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Path:   parsed.Path,
	}
	return strings.TrimRight(clean.String(), "/")
}

func modelPresetLabel(preset string) string {
	for _, item := range connectionPresets {
		if item.ID == preset {
			return item.Name
		}
	}
	return preset
}

func modelProtocolLabel(profile modelConnection, config distill.Config) string {
	switch config.Provider {
	case "gateway":
		return "本地网关 · Chat Completions"
	case "gateway-responses":
		return "本地网关 · Responses"
	case "openai":
		return "OpenAI Chat Completions"
	case "responses":
		return "OpenAI Responses"
	case "anthropic":
		return "Anthropic Messages"
	case "gemini":
		return "Google Gemini"
	case "compatible":
		return "OpenAI 兼容 Chat Completions"
	}
	if profile.Preset == "gateway" {
		if profile.GatewayAPI == "responses" {
			return "本地网关 · Responses"
		}
		return "本地网关 · Chat Completions"
	}
	return profile.APIProtocol
}

func formatDurationBrief(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func formatModelIdentity(profile modelConnection, config distill.Config) string {
	name := strings.TrimSpace(profile.Name)
	preset := modelPresetLabel(profile.Preset)
	if name == "" {
		name = preset
	} else if preset != "" && preset != name {
		name = fmt.Sprintf("%s（%s）", name, preset)
	}
	parts := []string{}
	if name != "" {
		parts = append(parts, "连接："+name)
	}
	if proto := modelProtocolLabel(profile, config); proto != "" {
		parts = append(parts, "协议："+proto)
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = strings.TrimSpace(profile.Model)
	}
	if model != "" {
		parts = append(parts, "模型："+model)
	}
	return strings.Join(parts, " · ")
}

func formatModelConnectionDetail(profile modelConnection, config distill.Config, hasKey bool, extras ...string) string {
	lines := []string{}
	if head := formatModelIdentity(profile, config); head != "" {
		lines = append(lines, head)
	}
	meta := []string{}
	base := safeLogURL(config.BaseURL)
	if base == "" {
		base = safeLogURL(profile.BaseURL)
	}
	if base != "" {
		meta = append(meta, "地址："+base)
	}
	if hasKey {
		meta = append(meta, "密钥：已配置")
	} else if profile.Preset == "gateway" || config.IsGateway() {
		meta = append(meta, "密钥：未配置（网关可选）")
	} else {
		meta = append(meta, "密钥：未配置")
	}
	if config.InputBudget > 0 || config.OutputBudget > 0 {
		meta = append(meta, fmt.Sprintf("预算：输入 %d / 输出 %d", config.InputBudget, config.OutputBudget))
	}
	if profile.Disabled {
		meta = append(meta, "状态：已停用")
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	for _, extra := range extras {
		if strings.TrimSpace(extra) != "" {
			lines = append(lines, extra)
		}
	}
	return strings.Join(lines, "\n")
}

func formatModelConnectionChanges(previous, current modelConnection, keyUpdated, keyCleared bool) string {
	changes := []string{}
	if previous.Name != current.Name {
		changes = append(changes, fmt.Sprintf("名称（%s → %s）", previous.Name, current.Name))
	}
	if previous.Preset != current.Preset {
		changes = append(changes, fmt.Sprintf("服务商（%s → %s）", modelPresetLabel(previous.Preset), modelPresetLabel(current.Preset)))
	}
	if previous.APIProtocol != current.APIProtocol || previous.GatewayAPI != current.GatewayAPI {
		changes = append(changes, "接口协议")
	}
	if previous.BaseURL != current.BaseURL {
		changes = append(changes, fmt.Sprintf("地址（%s → %s）", safeLogURL(previous.BaseURL), safeLogURL(current.BaseURL)))
	}
	if previous.Model != current.Model {
		oldModel := previous.Model
		if oldModel == "" {
			oldModel = "未设置"
		}
		newModel := current.Model
		if newModel == "" {
			newModel = "未设置"
		}
		changes = append(changes, fmt.Sprintf("默认模型（%s → %s）", oldModel, newModel))
	}
	if previous.InputBudget != current.InputBudget || previous.OutputBudget != current.OutputBudget {
		changes = append(changes, fmt.Sprintf("预算（%d/%d → %d/%d）", previous.InputBudget, previous.OutputBudget, current.InputBudget, current.OutputBudget))
	}
	if previous.Disabled != current.Disabled {
		if current.Disabled {
			changes = append(changes, "状态（启用 → 停用）")
		} else {
			changes = append(changes, "状态（停用 → 启用）")
		}
	}
	if keyCleared {
		changes = append(changes, "清除 API 密钥")
	} else if keyUpdated {
		changes = append(changes, "更新 API 密钥")
	}
	if len(changes) == 0 {
		return "操作：重新保存配置（参数未变化）"
	}
	return "变更项：" + strings.Join(changes, "、")
}

func formatAgentDisplayName(agent, variant string) string {
	if agent == "antigravity" {
		switch variant {
		case "cli":
			return "Antigravity CLI"
		case "app":
			return "Antigravity App"
		default:
			return "Antigravity"
		}
	}
	switch agent {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "pi":
		return "Pi"
	case "workbuddy":
		return "WorkBuddy"
	case "dsh":
		return "DeepSeek Harness"
	case "gemini":
		return "Gemini CLI"
	case "copilot":
		return "GitHub Copilot CLI"
	case "qwen":
		return "Qwen Code"
	default:
		return agent
	}
}

func formatScanEnvironment(env sessions.Environment) string {
	location := env.Platform
	switch env.Platform {
	case "windows":
		location = "Windows"
	case "linux":
		location = "Linux"
	case "darwin":
		location = "macOS"
	case "wsl":
		if env.WSL != "" {
			location = "WSL " + env.WSL
		} else {
			location = "WSL"
		}
	}
	state := env.State
	switch env.State {
	case "scanned":
		state = "已扫描"
	case "stopped":
		state = "已停止"
	case "unavailable":
		state = "不可访问"
	}
	if state != "" {
		return fmt.Sprintf("%s（%s）", location, state)
	}
	return location
}

func formatScanSummary(trigger string, records []sessions.Session, warnings []string, coverage sessions.ScanReport, added, updated, removed, customDirCount int, elapsed time.Duration) (string, string) {
	action := "扫描会话来源"
	switch trigger {
	case "startup":
		action = "初次扫描会话来源"
	case "manual":
		action = "手动刷新会话来源"
	case "sources":
		action = "更新扫描来源并重新扫描"
	case "auto":
		action = "增量扫描发现会话变更"
	}

	diffParts := []string{}
	if added > 0 {
		diffParts = append(diffParts, fmt.Sprintf("新增 %d 条", added))
	}
	if updated > 0 {
		diffParts = append(diffParts, fmt.Sprintf("更新 %d 条", updated))
	}
	if removed > 0 {
		diffParts = append(diffParts, fmt.Sprintf("移除 %d 条", removed))
	}
	summary := fmt.Sprintf("共发现 %d 条会话", len(records))
	if trigger != "startup" {
		if len(diffParts) > 0 {
			summary += "（" + strings.Join(diffParts, "，") + "）"
		} else {
			summary += "（无会话变更）"
		}
	}
	summary += fmt.Sprintf(" · 耗时 %s", formatDurationBrief(elapsed))
	if len(warnings) > 0 {
		summary += fmt.Sprintf(" · %d 条扫描提示", len(warnings))
	}

	lines := []string{summary}
	if trigger == "sources" {
		lines = append(lines, fmt.Sprintf("来源配置：已保存 %d 个自定义扫描目录", customDirCount))
	}

	if len(records) > 0 {
		agentCounts := map[string]int{}
		for _, item := range records {
			name := formatAgentDisplayName(item.Agent, item.AgentVariant)
			agentCounts[name]++
		}
		names := make([]string, 0, len(agentCounts))
		for name := range agentCounts {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			if agentCounts[names[i]] != agentCounts[names[j]] {
				return agentCounts[names[i]] > agentCounts[names[j]]
			}
			return names[i] < names[j]
		})
		agentParts := make([]string, 0, len(names))
		for _, name := range names {
			agentParts = append(agentParts, fmt.Sprintf("%s %d 条", name, agentCounts[name]))
		}
		lines = append(lines, "Agent 分布："+strings.Join(agentParts, " · "))
	}

	if len(coverage.Environments) > 0 || len(coverage.Roots) > 0 {
		envParts := make([]string, 0, len(coverage.Environments))
		for _, env := range coverage.Environments {
			envParts = append(envParts, formatScanEnvironment(env))
		}
		envLine := ""
		if len(envParts) > 0 {
			envLine = "扫描环境：" + strings.Join(envParts, "、")
		}
		if len(coverage.Roots) > 0 {
			activeRoots := 0
			for _, root := range coverage.Roots {
				if root.Sessions > 0 {
					activeRoots++
				}
			}
			rootText := fmt.Sprintf("检查 %d 个来源目录（%d 个有会话）", len(coverage.Roots), activeRoots)
			if envLine != "" {
				envLine += " · " + rootText
			} else {
				envLine = "扫描范围：" + rootText
			}
		}
		if envLine != "" {
			lines = append(lines, envLine)
		}
	}

	if len(warnings) > 0 {
		limit := min(len(warnings), 5)
		warnLines := make([]string, 0, limit+2)
		warnLines = append(warnLines, fmt.Sprintf("扫描提示（%d 条）：", len(warnings)))
		for i := range limit {
			warnLines = append(warnLines, "- "+warnings[i])
		}
		if len(warnings) > limit {
			warnLines = append(warnLines, fmt.Sprintf("- 其余 %d 条提示请前往「会话」页查看扫描覆盖详情", len(warnings)-limit))
		}
		lines = append(lines, strings.Join(warnLines, "\n"))
	}

	return action, strings.Join(lines, "\n")
}

