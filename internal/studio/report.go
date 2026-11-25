package studio

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Project   string `json:"project"`
}

type reportItem struct {
	Title   string         `json:"title"`
	Text    string         `json:"text"`
	Sources []reportSource `json:"sources"`
}

type reportSection struct {
	Title string       `json:"title"`
	Items []reportItem `json:"items"`
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
	headings    [3]string
	limit       int
}

var reportCategories = [...]distill.ReportCategory{distill.ReportOutcome, distill.ReportFinding, distill.ReportFollowup, distill.ReportQuestion}

var reportLayouts = map[string]reportLayout{
	"daily":   {"日报", "突出当日可核实的具体成果、阻碍和待办。", [3]string{"当日成果", "发现", "阻碍与待办"}, 6},
	"weekly":  {"周报", "跨项目按主题归纳进展，指出影响进度的阻碍和后续动作。", [3]string{"主题进展", "共性发现", "后续跟进"}, 8},
	"monthly": {"月报", "提炼有证据的里程碑和可复用经验，避免把日常操作当作里程碑。", [3]string{"里程碑与成果", "发现与经验", "后续跟进"}, 8},
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
		layout.headings[0] = "本期成果"
	} else if !endDay.Before(startDay.AddDate(0, 0, 28)) {
		layout = reportLayouts["monthly"]
	}
	layout.name = "报告"
	return layout, true
}

func (layout reportLayout) heading(index int) string {
	if index == len(layout.headings) {
		return "期间记录的问题"
	}
	return layout.headings[index]
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
	cached, err := a.readReviews(r.Context())
	if err != nil {
		http.Error(w, "无法读取工作回顾", http.StatusInternalServerError)
		return
	}
	items, revisions := a.reviewSnapshot()
	result := workReport{Title: "工作" + layout.name, Mode: "local"}
	var groups []reportFactGroup
	for _, item := range items {
		if archived[item.ID] || !inReviewScope(item, query) || !slices.ContainsFunc(item.Messages, func(message sessions.Message) bool {
			return inReviewWindow(message.Timestamp, from, to)
		}) {
			continue
		}
		result.Coverage.Active++
		checkpoint, valid := cached[item.ID].verified(item.Messages, revisions[item.ID])
		if !valid {
			continue
		}
		if checkpoint.Consumed == len(item.Messages) {
			result.Coverage.Ready++
		}
		roles := make(map[string]string, checkpoint.Consumed)
		during := make(map[string]bool)
		for _, message := range item.Messages[:checkpoint.Consumed] {
			roles[message.ID] = message.Role
			if inReviewWindow(message.Timestamp, from, to) {
				during[message.ID] = true
			}
		}
		group := reportFactGroup{session: item, project: [3]string{item.Platform, item.WSL, projectPath(item)}}
		for _, topic := range distill.ConsolidateReviewTopics(checkpoint.Topics) {
			entry, found := reportTopic(topic, roles, during)
			if !found {
				continue
			}
			fact := reportFact{topic: entry, sources: make([]reportSource, 0, len(entry.Evidence))}
			for _, id := range entry.Evidence {
				fact.sources = append(fact.sources, reportSource{item.ID, id, item.Project})
			}
			group.facts = append(group.facts, fact)
			result.Coverage.Topics++
		}
		if len(group.facts) > 0 {
			groups = append(groups, group)
		}
	}
	if connectionID != "" {
		ctx := r.Context()
		switch {
		case a.demo:
			result.Notice = "演示工作区使用本地汇总。"
		case len(groups) == 0:
			result.Notice = "期间没有可供模型综合的工作回顾，已使用本地汇总。"
		default:
			config, err := a.connectionConfig(connectionID)
			if err == nil {
				facts, sources := prepareReportFacts(groups)
				synthesis, synthErr := distill.SynthesizeReport(ctx, config, selectedModel(ctx, config), layout.focus, layout.limit, facts)
				if synthErr == nil {
					result.Mode, result.Summary = "model", synthesis.Summary
					for index, category := range reportCategories {
						pos := slices.IndexFunc(synthesis.Sections, func(group distill.ReportSection) bool { return group.Category == category })
						if pos < 0 {
							continue
						}
						section := synthesis.Sections[pos]
						entry := reportSection{Title: layout.heading(index)}
						for _, item := range section.Items {
							row := reportItem{Title: item.Title, Text: item.Text}
							for _, index := range item.Sources {
								row.Sources = mergeReportSources(row.Sources, sources[index-1])
							}
							entry.Items = append(entry.Items, row)
						}
						result.Sections = append(result.Sections, entry)
					}
					if synthesis.Shortened {
						result.Notice = "模型输入达到预算上限，部分工作回顾已缩短或省略。"
					}
				}
			}
			if result.Mode == "local" {
				result.Notice = "模型综合暂不可用，已使用本地汇总。"
			}
		}
	}
	if result.Mode == "local" {
		var shortened bool
		result.Sections, shortened = localReportSections(groups, layout)
		if shortened {
			result.Notice = strings.TrimSpace(result.Notice + " 本地预览已达到篇幅上限，覆盖统计包含全部符合条件的回顾。")
		}
	}
	result.Markdown = renderWorkReport(result, from, to)
	jsonReply(w, result)
}

func reportTopic(topic distill.Topic, roles map[string]string, during map[string]bool) (distill.Topic, bool) {
	if len(topic.ReviewHistory) == 0 {
		return reportContribution(topic, roles, during)
	}
	var entry distill.Topic
	found := false
	for _, change := range topic.ReviewHistory {
		part, include := reportContribution(distill.Topic{
			Title: topic.Problem, Problem: topic.Problem, Approach: change.Approach,
			Outcome: change.Outcome, Status: change.Status, Evidence: change.Evidence,
		}, roles, during)
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

func reportContribution(topic distill.Topic, roles map[string]string, during map[string]bool) (distill.Topic, bool) {
	var questions, answers []string
	answerOutside := false
	for _, id := range topic.Evidence {
		switch roles[id] {
		case "user":
			if during[id] {
				questions = append(questions, id)
			}
		case "assistant":
			if during[id] {
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
	if answerOutside || len(answers) == 0 && (topic.Approach != "" || topic.Outcome != "") {
		topic.Approach, topic.Outcome, topic.Status = "", "", ""
		topic.Title = topic.Problem
	}
	topic.Evidence = questions
	return topic, true
}

func localReportSections(groups []reportFactGroup, layout reportLayout) ([]reportSection, bool) {
	type section struct {
		items  []reportItem
		titles map[string]int
		texts  map[string]int
	}
	var sections [len(reportCategories)]section
	for i := range sections {
		sections[i].titles = make(map[string]int)
		sections[i].texts = make(map[string]int)
	}
	shortened := false
	for _, group := range groups {
		for _, fact := range group.facts {
			topic := fact.topic
			category := distill.ReportCategoryFor(topic.Status, topic.Outcome)
			index := slices.Index(reportCategories[:], category)
			parts := []string{topic.Outcome, topic.Approach, topic.Problem}
			switch category {
			case distill.ReportOutcome:
				parts = []string{topic.Outcome}
			case distill.ReportFollowup:
				parts = []string{topic.Outcome, topic.Approach, topic.Problem}
			case distill.ReportQuestion:
				parts = []string{topic.Problem}
			}
			title := reportLine(topic.Title)
			if title == "" {
				title = reportLine(topic.Problem)
			}
			if title == "" {
				continue
			}
			title, clipped := limitReportLine(title, 90)
			shortened = shortened || clipped
			bucket, titleKey := &sections[index], reportTextKey(title)
			row, exists := bucket.titles[titleKey]
			var text []string
			for _, part := range parts {
				part = reportLine(part)
				if key := reportTextKey(part); key != "" && key != titleKey && !slices.ContainsFunc(text, func(previous string) bool { return reportTextKey(previous) == key }) {
					text = append(text, part)
				}
			}
			if !exists && len(text) > 0 {
				row, exists = bucket.texts[reportTextKey(text[0])]
			}
			if !exists {
				if len(bucket.items) == layout.limit {
					shortened = true
					continue
				}
				row = len(bucket.items)
				bucket.items = append(bucket.items, reportItem{Title: title})
			}
			item := &bucket.items[row]
			supported := len(text) == 0 && item.Title == title
			for _, part := range text {
				key := reportTextKey(part)
				if slices.ContainsFunc(strings.Split(item.Text, " / "), func(previous string) bool { return reportTextKey(previous) == key }) {
					supported = true
					continue
				}
				separator := ""
				if item.Text != "" {
					separator = " / "
				}
				space := 180 - utf8.RuneCountInString(item.Text) - len(separator)
				if space <= 0 {
					shortened = true
					break
				}
				part, clipped := limitReportLine(part, space)
				shortened = shortened || clipped
				item.Text += separator + part
				bucket.texts[key] = row
				supported = true
				break
			}
			if supported {
				bucket.titles[titleKey] = row
				item.Sources = mergeReportSources(item.Sources, fact.sources)
			}
		}
	}
	result := []reportSection{}
	remaining := layout.limit
	for index, section := range sections {
		if len(section.items) == 0 {
			continue
		}
		reserved := 0
		for _, later := range sections[index+1:] {
			if len(later.items) > 0 {
				reserved++
			}
		}
		count := min(len(section.items), remaining-reserved)
		shortened = shortened || count < len(section.items)
		result = append(result, reportSection{Title: layout.heading(index), Items: section.items[:count]})
		remaining -= count
	}
	return result, shortened
}

func reportLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func reportTextKey(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text)
}

func limitReportLine(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	return string([]rune(text)[:limit-1]) + "…", true
}

func mergeReportSources(base, sources []reportSource) []reportSource {
	for _, source := range sources {
		if len(base) == 24 {
			break
		}
		if !slices.Contains(base, source) {
			base = append(base, source)
		}
	}
	return base
}

var reportPrivateText = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\|(?:\.\.?|~)?/)[\pL\pN_@.+=%$~:/\\-]+|[\pL\pN_@.-]+(?:[\\/][\pL\pN_@.+-]+)+|\b[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\b`)

func prepareReportFacts(groups []reportFactGroup) ([]distill.ReportFact, [][]reportSource) {
	var projects [][]reportFactGroup
	byProject := map[[3]string]int{}
	for _, group := range groups {
		index, exists := byProject[group.project]
		if !exists {
			index = len(projects)
			byProject[group.project] = index
			projects = append(projects, nil)
		}
		projects[index] = append(projects[index], group)
	}
	type cursor struct{ group, fact int }
	positions := make([]cursor, len(projects))
	var facts []distill.ReportFact
	var sources [][]reportSource
	for {
		before := len(facts)
		for i, groups := range projects {
			p := &positions[i]
			if p.group >= len(groups) {
				continue
			}
			group := groups[p.group]
			fact := group.facts[p.fact]
			p.fact++
			if p.fact == len(group.facts) {
				p.group, p.fact = p.group+1, 0
			}
			item := group.session
			values := []string{item.Path, item.SourceRoot, item.Cwd, item.ProjectPath, item.ProjectDisplayPath, item.ID, item.ParentID}
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
			topic := fact.topic
			facts = append(facts, distill.ReportFact{
				Title: clean(topic.Title), Problem: clean(topic.Problem), Approach: clean(topic.Approach),
				Outcome: clean(topic.Outcome), Status: topic.Status,
			})
			sources = append(sources, fact.sources)
		}
		if len(facts) == before {
			return facts, sources
		}
	}
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
		fmt.Fprintf(&output, "\n## %s\n\n", escape(section.Title))
		for _, item := range section.Items {
			fmt.Fprintf(&output, "- **%s**", escape(item.Title))
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
	fmt.Fprintf(&output, "\n已提炼 %d / %d 条期间会话，收录 %d 个主题。\n", report.Coverage.Ready, report.Coverage.Active, report.Coverage.Topics)
	if report.Mode == "model" {
		output.WriteString("\n模型草稿，请按依据核对关键结论。\n")
	}
	if report.Notice != "" {
		fmt.Fprintf(&output, "\n%s\n", report.Notice)
	}
	return output.String()
}
