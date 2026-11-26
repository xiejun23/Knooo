package studio

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

type reportProject struct {
	Project     string `json:"project"`
	ProjectPath string `json:"projectPath"`
	Platform    string `json:"platform"`
	WSL         string `json:"wsl,omitempty"`
}

func (a *app) reportProjects(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, to, err := reviewWindow(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.ensureSessionIndex()
	archived, err := a.archivedSessions(r.Context())
	if err != nil {
		http.Error(w, "无法读取会话归档", http.StatusInternalServerError)
		return
	}
	items, _ := a.reviewSnapshot()
	projects := []reportProject{}
	seen := make(map[[3]string]bool)
	for _, item := range items {
		path := projectPath(item)
		key := [3]string{item.Platform, item.WSL, path}
		if path == "" || seen[key] || archived[item.ID] || !item.MatchesAgent(query["agent"]...) {
			continue
		}
		if slices.ContainsFunc(item.Messages, func(message sessions.Message) bool {
			return inReviewWindow(message.Timestamp, from, to)
		}) {
			projects = append(projects, reportProject{item.Project, path, item.Platform, item.WSL})
			seen[key] = true
		}
	}
	jsonReply(w, projects)
}

type reportSource struct {
	SessionID       string `json:"sessionId"`
	MessageID       string `json:"messageId"`
	Project         string `json:"project"`
	EvidenceVersion string `json:"evidenceVersion,omitempty"`
}

type reportItem struct {
	Title   string         `json:"title"`
	Text    string         `json:"text"`
	Status  string         `json:"status,omitempty"`
	Sources []reportSource `json:"sources"`
}

type reportSection struct {
	Title        string       `json:"title"`
	Items        []reportItem `json:"items"`
	Supplemental bool         `json:"supplemental,omitempty"`
}

type reportCoverage struct {
	Ready  int `json:"ready"`
	Active int `json:"active"`
	Topics int `json:"topics"`
}

type workReport struct {
	Title    string          `json:"title"`
	Summary  string          `json:"summary,omitempty"`
	Sections []reportSection `json:"sections"`
	Coverage reportCoverage  `json:"coverage"`
	Mode     string          `json:"mode"`
	Notice   string          `json:"notice,omitempty"`
	Markdown string          `json:"markdown"`
}

type reportFact struct {
	topic   distill.Topic
	sources []reportSource
}

type reportFactGroup struct {
	project [3]string
	session sessions.Session
	facts   []reportFact
}

type reportLayout struct {
	name, focus string
	limit       int
}

var reportTypes = [...]struct {
	kind  distill.ReportType
	title string
}{
	{distill.ReportFeature, "需求与功能开发"},
	{distill.ReportBugfix, "缺陷排查与修复"},
	{distill.ReportTesting, "测试联调与验收"},
	{distill.ReportResearch, "学习与调研探索"},
	{distill.ReportOps, "优化与工程维护"},
	{distill.ReportOther, "其他事项"},
	{distill.ReportQuestion, "期间记录的问题"},
}

var reportLayouts = map[string]reportLayout{
	"daily":   {"日报", "按事项写清当日结果和下一步。", 15},
	"weekly":  {"周报", "归并同一事项的跨天进展，写明结果和下一步。", 12},
	"monthly": {"月报", "优先提炼有证据的里程碑、可复用经验和后续重点。", 8},
}

func reportLayoutFor(kind string, from, to time.Time) (reportLayout, bool) {
	if kind != "custom" {
		layout, ok := reportLayouts[kind]
		return layout, ok
	}
	layout := reportLayouts["weekly"]
	startDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	if !endDay.After(startDay.AddDate(0, 0, 3)) {
		layout = reportLayouts["daily"]
	} else if !endDay.Before(startDay.AddDate(0, 0, 28)) {
		layout = reportLayouts["monthly"]
	}
	layout.name = "报告"
	return layout, true
}

func (a *app) report(w http.ResponseWriter, r *http.Request) {
	var connectionID string
	if r.Method == http.MethodPost {
		var body *struct {
			ConnectionID *string `json:"connectionId"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body == nil || body.ConnectionID == nil {
			http.Error(w, "请求格式无效", http.StatusBadRequest)
			return
		}
		connectionID = strings.TrimSpace(*body.ConnectionID)
	}
	query := r.URL.Query()
	from, to, err := reviewWindow(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	layout, ok := reportLayoutFor(query.Get("kind"), from, to)
	if !ok {
		http.Error(w, "无效的报告类型", http.StatusBadRequest)
		return
	}
	a.ensureSessionIndex()
	archived, err := a.archivedSessions(r.Context())
	if err != nil {
		http.Error(w, "无法读取会话归档", http.StatusInternalServerError)
		return
	}
	items, revisions := a.reviewSnapshot()
	var scoped []sessions.Session
	var scopedIDs []string
	for _, item := range items {
		if archived[item.ID] || !inReviewScope(item, query) || !slices.ContainsFunc(item.Messages, func(message sessions.Message) bool {
			return inReviewWindow(message.Timestamp, from, to)
		}) {
			continue
		}
		scoped = append(scoped, item)
		scopedIDs = append(scopedIDs, item.ID)
	}
	cached, err := a.readReviewsFor(r.Context(), scopedIDs)
	if err != nil {
		http.Error(w, "无法读取工作回顾", http.StatusInternalServerError)
		return
	}
	result := workReport{Title: "工作" + layout.name, Mode: "local", Sections: []reportSection{}, Coverage: reportCoverage{Active: len(scoped)}}
	var groups []reportFactGroup
	for _, item := range scoped {
		checkpoint, valid := cached[item.ID].verified(item.Messages, revisions[item.ID])
		if !valid {
			continue
		}
		if checkpoint.Consumed == len(item.Messages) {
			result.Coverage.Ready++
		}
		roles := make(map[string]string, checkpoint.Consumed)
		during := make(map[string]int)
		for index, message := range item.Messages[:checkpoint.Consumed] {
			roles[message.ID] = message.Role
			if inReviewWindow(message.Timestamp, from, to) {
				during[message.ID] = index
			}
		}
		versions := make(map[string]string)
		group := reportFactGroup{session: item, project: [3]string{item.Platform, item.WSL, projectPath(item)}}
		for _, topic := range checkpoint.Topics {
			entry, found := reportTopic(topic, roles, during)
			if !found || reportNoise(entry) {
				continue
			}
			fact := reportFact{topic: entry, sources: make([]reportSource, 0, len(entry.Evidence))}
			for _, id := range entry.Evidence {
				version, exists := versions[id]
				if !exists {
					version = messageEvidenceVersion(item.Messages[during[id]])
					versions[id] = version
				}
				fact.sources = append(fact.sources, reportSource{item.ID, id, item.Project, version})
			}
			group.facts = append(group.facts, fact)
			result.Coverage.Topics++
		}
		if len(group.facts) > 0 {
			groups = append(groups, group)
		}
	}
	facts := prepareReportFacts(groups, connectionID != "" && !a.demo)
	var synthesis distill.ReportSynthesis
	if connectionID != "" {
		ctx := r.Context()
		switch {
		case a.demo:
			result.Notice = "演示工作区使用本地汇总。"
		case len(facts) == 0:
			result.Notice = "期间没有可供模型综合的工作回顾，已使用本地汇总。"
		default:
			config, err := a.connectionConfig(connectionID)
			if err == nil {
				input := make([]distill.ReportFact, len(facts))
				for i, fact := range facts {
					input[i] = fact.model
				}
				synthesis, err = distill.SynthesizeReport(ctx, config, selectedModel(ctx, config), layout.focus, layout.limit, input)
				if err == nil {
					result.Mode = "model"
				}
			}
			if result.Mode == "local" {
				result.Notice = "模型综合暂不可用，已使用本地汇总。"
			}
		}
	}
	var supplemented int
	result.Sections, supplemented = reportSections(facts, synthesis.Items, layout.limit)
	if result.Mode == "model" {
		switch {
		case supplemented > 0:
			result.Notice = fmt.Sprintf("已用本地回顾补齐模型未覆盖的 %d 项事项。", supplemented)
		case !synthesis.Shortened:
			result.Summary = synthesis.Summary
		}
	}
	result.Markdown = renderWorkReport(result, from, to)
	if r.Method == http.MethodPost {
		if err := a.storeGeneratedReport(r.Context(), reportDocumentScope(query, from, to), result); err != nil {
			http.Error(w, "无法保存生成的报告", http.StatusInternalServerError)
			return
		}
	}
	jsonReply(w, result)
}

func reportTopic(topic distill.Topic, roles map[string]string, during map[string]int) (distill.Topic, bool) {
	if len(topic.ReviewHistory) == 0 {
		return reportContribution(topic, roles, during, false)
	}
	var entry distill.Topic
	found := false
	for index, change := range topic.ReviewHistory {
		part, include := reportContribution(distill.Topic{
			Title: topic.Problem, Problem: topic.Problem, Approach: change.Approach,
			Outcome: change.Outcome, Status: change.Status, Evidence: change.Evidence,
		}, roles, during, index > 0)
		if !include {
			continue
		}
		if !found {
			entry, found = part, true
			continue
		}
		entry.ApplyReviewContribution(part.Approach, part.Outcome, part.Status)
		for _, id := range part.Evidence {
			if !slices.Contains(entry.Evidence, id) {
				entry.Evidence = append(entry.Evidence, id)
			}
		}
	}
	return entry, found
}

func reportContribution(topic distill.Topic, roles map[string]string, during map[string]int, continuation bool) (distill.Topic, bool) {
	var questions, answers []string
	answerOutside := false
	for _, id := range topic.Evidence {
		_, inWindow := during[id]
		switch roles[id] {
		case "user":
			if inWindow {
				questions = append(questions, id)
			}
		case "assistant":
			if inWindow {
				answers = append(answers, id)
			} else {
				answerOutside = true
			}
		}
	}
	if len(answers) > 0 && !answerOutside && (topic.Approach != "" || topic.Outcome != "" || topic.Status == "open") {
		topic.Evidence = append(questions, answers...)
		return topic, true
	}
	if len(questions) == 0 {
		return distill.Topic{}, false
	}
	if continuation && !answerOutside && topic.Status == "open" && topic.Approach == "" && topic.Outcome == "" {
		topic.Evidence = questions
		return topic, true
	}
	problem := strings.TrimSpace(topic.Problem)
	if answerOutside || topic.Approach != "" || topic.Outcome != "" || topic.Status == "open" ||
		strings.HasSuffix(problem, "？") || strings.HasSuffix(problem, "?") {
		topic.Approach, topic.Outcome, topic.Status = "", "", ""
		if problem != "" {
			topic.Title = problem
		}
	}
	topic.Evidence = questions
	return topic, true
}

func reportLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

var reportPrivateText = regexp.MustCompile(`(?i)(?:[a-z]:[\/]|\\|(?:\.\.?|~)?/)[\pL\pN_@.+=%$~:/\\-]+|[\pL\pN_@.-]+(?:[\/][\pL\pN_@.+-]+)+|\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b`)

type reportEntry struct {
	local   reportItem
	model   distill.ReportFact
	project int
}

func prepareReportFacts(groups []reportFactGroup, forModel bool) []reportEntry {
	type workKey struct {
		project [3]string
		problem string
	}
	work := map[workKey]int{}
	projects := map[[3]string]int{}
	type projectFacts struct {
		core, other []reportEntry
	}
	var buckets []projectFacts
	type duplicateKey struct {
		work                              int
		title, problem, approach, outcome string
		status                            string
	}
	dedup := map[duplicateKey]int{}
	var entries []reportEntry
	var projectNames []string
	if forModel {
		seen := make(map[string]bool)
		for _, group := range groups {
			if name := group.session.Project; name != "" && !seen[name] {
				projectNames = append(projectNames, name)
				seen[name] = true
			}
		}
	}
	for _, group := range groups {
		project, known := group.project, group.project[2] != ""
		index, exists := projects[project]
		if !exists {
			index = len(buckets)
			buckets = append(buckets, projectFacts{})
			projects[project] = index
		}
		for _, fact := range group.facts {
			topic := fact.topic
			entry := reportEntry{local: localReportItem(topic, fact.sources), project: index}
			var workID int
			if key := (workKey{project, reportLine(topic.Problem)}); known && topic.Status != "" && key.problem != "" {
				workID = work[key]
				if workID == 0 {
					workID = len(work) + 1
					work[key] = workID
				}
				key := duplicateKey{workID, topic.Title, topic.Problem, topic.Approach, topic.Outcome, topic.Status}
				if previous, exists := dedup[key]; exists {
					old := &entries[previous].local
					old.Sources = mergeReportSources(old.Sources, fact.sources)
					continue
				}
				dedup[key] = len(entries)
			}

			entry.model = distill.ReportFact{Status: topic.Status, Type: reportTypeFor(topic), Work: workID}
			if forModel {
				fillReportModelFact(&entry.model, group.session, fact, projectNames)
				if known {
					entry.model.Project = fmt.Sprintf("P%d", index+1)
				}
			}
			entries = append(entries, entry)
		}
	}
	for _, entry := range entries {
		bucket := &buckets[entry.project]
		if primaryReportStatus(entry.model.Status) {
			bucket.core = append(bucket.core, entry)
		} else {
			bucket.other = append(bucket.other, entry)
		}
	}
	ordered := make([]reportEntry, 0, len(entries))
	for _, core := range []bool{true, false} {
		for round := 0; ; round++ {
			before := len(ordered)
			for _, bucket := range buckets {
				facts := bucket.other
				if core {
					facts = bucket.core
				}
				if round < len(facts) {
					ordered = append(ordered, facts[round])
				}
			}
			if before == len(ordered) {
				break
			}
		}
	}
	return ordered
}

func fillReportModelFact(model *distill.ReportFact, item sessions.Session, fact reportFact, projectNames []string) {
	values := []string{item.Path, item.SourceRoot, item.Cwd, item.ProjectPath, item.ProjectDisplayPath, item.ID, item.ParentID}
	values = append(values, projectNames...)
	for _, source := range fact.sources {
		values = append(values, source.MessageID)
	}
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	var replacements []string
	for _, value := range values {
		if value != "" {
			replacements = append(replacements, value, "[本地信息]")
		}
	}
	redact := strings.NewReplacer(replacements...)
	clean := func(text string) string {
		return reportPrivateText.ReplaceAllString(reportLine(redact.Replace(text)), "[本地信息]")
	}
	model.Title = clean(fact.topic.Title)
	model.Problem = clean(fact.topic.Problem)
	model.Approach = clean(fact.topic.Approach)
	model.Outcome = clean(fact.topic.Outcome)
}

func reportNoise(topic distill.Topic) bool {
	if topic.Status == "open" || topic.Status == "" || topic.Outcome != "" {
		return false
	}
	for _, text := range []string{topic.Title, topic.Problem, topic.Approach} {
		switch strings.ToLower(strings.Trim(reportLine(text), "。.!！")) {
		case "", "提交代码", "暂存代码", "推送代码", "切换分支", "查看目录", "确认环境", "git status", "git add", "git commit", "git push":
		default:
			return false
		}
	}
	return true
}

func reportTypeFor(topic distill.Topic) distill.ReportType {
	if topic.Status == "" {
		return distill.ReportQuestion
	}
	text := strings.ToLower(topic.Title + " " + topic.Problem)
	for _, rule := range []struct {
		kind  distill.ReportType
		terms []string
	}{
		{distill.ReportBugfix, []string{"缺陷", "bug", "修复", "排查", "异常", "崩溃", "回归失败"}},
		{distill.ReportResearch, []string{"调研", "学习", "对比方案", "技术选型", "研究"}},
		{distill.ReportOps, []string{"优化", "重构", "部署", "迁移", "升级", "发布", "配置", "运维", "维护", "性能"}},
		{distill.ReportTesting, []string{"测试", "联调", "验收", "回归", "压测"}},
		{distill.ReportFeature, []string{"需求", "新增", "功能", "实现", "开发", "支持"}},
	} {
		for _, term := range rule.terms {
			if strings.Contains(text, term) {
				return rule.kind
			}
		}
	}
	return distill.ReportOther
}

func reportStatus(status string) string {
	switch status {
	case "verified":
		return "已验证"
	case "resolved":
		return "已解决"
	case "open":
		return "待跟进"
	case "":
		return "待确认"
	default:
		return "已观察"
	}
}

func localReportItem(topic distill.Topic, sources []reportSource) reportItem {
	title := reportLine(topic.Title)
	if title == "" {
		title = reportLine(topic.Problem)
	}
	var text []string
	fields := []string{topic.Outcome, topic.Approach, topic.Problem}
	if topic.Status == "verified" || topic.Status == "resolved" {
		fields = []string{topic.Outcome, topic.Approach}
	} else if topic.Status == "" {
		fields = []string{topic.Problem}
	}
	for _, field := range fields {
		if part := reportLine(field); part != "" && part != title && !slices.Contains(text, part) {
			text = append(text, part)
		}
	}
	return reportItem{Title: title, Text: strings.Join(text, " / "), Status: reportStatus(topic.Status), Sources: sources}
}

func mergeReportSources(base, sources []reportSource) []reportSource {
	for _, source := range sources {
		if !slices.Contains(base, source) {
			base = append(base, source)
		}
	}
	return base
}

func primaryReportStatus(status string) bool {
	return status == "verified" || status == "resolved" || status == "open"
}

func reportSections(facts []reportEntry, items []distill.ReportItem, limit int) ([]reportSection, int) {
	type typedRow struct {
		kind distill.ReportType
		core bool
		item reportItem
	}
	var rows []typedRow
	included := make([]bool, len(facts))
	for _, item := range items {
		row := typedRow{kind: item.Type, item: reportItem{Title: item.Title, Text: item.Text}}
		var statuses []string
		for _, source := range item.Sources {
			index := source - 1
			fact := facts[index]
			included[index] = true
			row.core = row.core || primaryReportStatus(fact.model.Status)
			row.item.Sources = mergeReportSources(row.item.Sources, fact.local.Sources)
			if !slices.Contains(statuses, fact.local.Status) {
				statuses = append(statuses, fact.local.Status)
			}
			if fact.model.Status == "open" {
				pending := fact.local.Text
				if pending == "" {
					pending = fact.local.Title
				}
				if !strings.Contains(row.item.Text, pending) {
					row.item.Text += " 待跟进：" + pending
				}
			}
		}
		row.item.Status = strings.Join(statuses, "、")
		rows = append(rows, row)
	}
	supplemented := 0
	localWork := make(map[int]int)
	merged := make(map[int]bool)
	for i, fact := range facts {
		if included[i] {
			continue
		}
		supplemented++
		kind, work := fact.model.Type, fact.model.Work
		if previous, exists := localWork[work]; exists {
			row := &rows[previous]
			if !merged[work] {
				row.item.Text = reportStatusDetail(row.item)
				merged[work] = true
			}
			row.item.Text += " / " + reportStatusDetail(fact.local)
			if !slices.Contains(strings.Split(row.item.Status, "、"), fact.local.Status) {
				row.item.Status += "、" + fact.local.Status
			}
			if row.kind == distill.ReportOther {
				row.kind = kind
			}
			row.core = row.core || primaryReportStatus(fact.model.Status)
			row.item.Sources = mergeReportSources(row.item.Sources, fact.local.Sources)
			continue
		}
		if work != 0 {
			localWork[work] = len(rows)
		}
		rows = append(rows, typedRow{kind, primaryReportStatus(fact.model.Status), fact.local})
	}
	main, supplemental := make(map[distill.ReportType][]reportItem), make(map[distill.ReportType][]reportItem)
	for _, core := range []bool{true, false} {
		for _, row := range rows {
			if row.core != core {
				continue
			}
			if limit > 0 {
				main[row.kind] = append(main[row.kind], row.item)
				limit--
			} else {
				supplemental[row.kind] = append(supplemental[row.kind], row.item)
			}
		}
	}
	result := []reportSection{}
	for _, section := range reportTypes {
		if rows := main[section.kind]; len(rows) > 0 {
			result = append(result, reportSection{Title: section.title, Items: rows})
		}
	}
	for _, section := range reportTypes {
		if rows := supplemental[section.kind]; len(rows) > 0 {
			result = append(result, reportSection{Title: section.title, Items: rows, Supplemental: true})
		}
	}
	return result, supplemented
}

func reportStatusDetail(item reportItem) string {
	if item.Text == "" {
		return item.Status + "：" + item.Title
	}
	return item.Status + "：" + item.Title + " · " + item.Text
}

var reportMarkdownEscapes = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "&", `\&`, "#", `\#`, "!", `\!`, "~", `\~`, "|", `\|`, "-", `\-`, "+", `\+`, "=", `\=`, ".", `\.`, "(", `\(`, ")", `\)`)

func renderWorkReport(report workReport, from, to time.Time) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# %s\n\n%s 至 %s\n", report.Title, from.Format(time.DateOnly), to.Add(-time.Nanosecond).Format(time.DateOnly))
	escape := reportMarkdownEscapes.Replace
	if report.Summary != "" {
		fmt.Fprintf(&output, "\n%s\n", escape(report.Summary))
	}
	for _, section := range report.Sections {
		heading := section.Title
		if section.Supplemental {
			heading = "补充事项 · " + heading
		}
		fmt.Fprintf(&output, "\n## %s\n\n", escape(heading))
		for _, item := range section.Items {
			fmt.Fprintf(&output, "- **%s**", escape(item.Title))
			if item.Status != "" {
				fmt.Fprintf(&output, " · %s", escape(item.Status))
			}
			if item.Text != "" {
				fmt.Fprintf(&output, " · %s", escape(item.Text))
			}
			var projects []string
			for _, source := range item.Sources {
				if source.Project != "" && !slices.Contains(projects, source.Project) {
					projects = append(projects, source.Project)
				}
			}
			if len(projects) > 0 {
				fmt.Fprintf(&output, "（%s）", escape(reportLine(strings.Join(projects, "、"))))
			}
			output.WriteByte('\n')
		}
	}
	fmt.Fprintf(&output, "\n已提炼 %d / %d 条期间会话，共 %d 项期间回顾。\n", report.Coverage.Ready, report.Coverage.Active, report.Coverage.Topics)
	if report.Mode == "model" {
		output.WriteString("\n模型草稿，请按依据核对关键结论。\n")
	}
	if report.Notice != "" {
		fmt.Fprintf(&output, "\n%s\n", report.Notice)
	}
	return output.String()
}
