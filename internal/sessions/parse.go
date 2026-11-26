package sessions

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type record struct {
	kind, id, parent, cwd, title string
	at, started                  time.Time
	message                      Message
	gemini                       *geminiChange
}

type utterance struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp json.RawMessage `json:"timestamp"`
}

func parseRecord(agent string, line []byte, number int) (record, bool) {
	if number == 1 {
		line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
	}
	if len(bytes.TrimSpace(line)) == 0 {
		return record{}, true
	}
	if agent == "gemini" {
		return parseGemini(line, number)
	}
	if agent == "dsh" {
		return parseDSH(line, number)
	}
	var e struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		ParentID  string          `json:"parentId"`
		Cwd       string          `json:"cwd"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		Timestamp json.RawMessage `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
		Message   json.RawMessage `json:"message"`
		Data      json.RawMessage `json:"data"`
		Subtype   string          `json:"subtype"`
		System    struct {
			Title  string `json:"customTitle"`
			Source string `json:"titleSource"`
			Parent string `json:"parentSessionId"`
		} `json:"systemPayload"`

		SessionID   string `json:"sessionId"`
		UUID        string `json:"uuid"`
		CustomTitle string `json:"customTitle"`
		AITitle     string `json:"aiTitle"`
		Summary     string `json:"summary"`
		IsMeta      bool   `json:"isMeta"`
		ForkedFrom  struct {
			SessionID string `json:"sessionId"`
		} `json:"forkedFrom"`

		Name          string `json:"name"`
		ParentSession string `json:"parentSession"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		return record{}, false
	}

	r := record{kind: e.Type, at: parseTime(e.Timestamp)}
	var role, text, messageID string
	switch agent {
	case "codex":
		var p struct {
			Type       string          `json:"type"`
			ID         string          `json:"id"`
			ThreadID   string          `json:"thread_id"`
			ParentID   string          `json:"parent_thread_id"`
			ForkedFrom string          `json:"forked_from_id"`
			Cwd        string          `json:"cwd"`
			Title      string          `json:"title"`
			Timestamp  json.RawMessage `json:"timestamp"`
			Source     json.RawMessage `json:"source"`
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			Message    json.RawMessage `json:"message"`
		}
		if len(e.Payload) != 0 && json.Unmarshal(e.Payload, &p) != nil {
			return record{}, false
		}
		switch e.Type {
		case "session_meta":
			var origin struct {
				Subagent struct {
					ThreadSpawn struct {
						ParentID string `json:"parent_thread_id"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			}
			// Source is a string for top-level sessions and an object for spawns.
			_ = json.Unmarshal(p.Source, &origin)
			r.id = firstNonEmpty(p.ID, p.ThreadID)
			r.parent = firstNonEmpty(p.ParentID, p.ForkedFrom, origin.Subagent.ThreadSpawn.ParentID)
			r.cwd, r.title, r.started = p.Cwd, p.Title, parseTime(p.Timestamp)
		case "turn_context":
			r.cwd = p.Cwd
		case "response_item":
			if p.Type == "message" {
				role, text, messageID = p.Role, contentText(p.Content), p.ID
			}
		case "event_msg":
			switch p.Type {
			case "user_message":
				role, text = "user", contentText(p.Message)
			case "agent_message":
				role, text = "assistant", contentText(p.Message)
			}
		}
		if role == "user" {
			text = stripCodexContext(text)
		}
	case "claude":
		r.id, r.cwd = e.SessionID, e.Cwd
		r.parent = e.ForkedFrom.SessionID
		switch e.Type {
		case "custom-title":
			r.title = e.CustomTitle
		case "ai-title":
			r.title = e.AITitle
		case "summary":
			r.title = e.Summary
		case "user", "assistant":
			if !e.IsMeta {
				var m utterance
				if json.Unmarshal(e.Message, &m) == nil {
					role, text, messageID = e.Type, contentText(m.Content), e.UUID
				}
			}
		}
	case "qwen":
		r.id, r.cwd, r.parent = e.SessionID, e.Cwd, e.System.Parent
		if e.Subtype == "custom_title" {
			r.kind, r.title = "custom-title", e.System.Title
			if e.System.Source == "auto" {
				r.kind = "ai-title"
			}
		}
		if e.Type == "assistant" || e.Type == "user" && (e.Subtype == "" || e.Subtype == "mid_turn_user_message") {
			var m struct {
				Parts json.RawMessage `json:"parts"`
			}
			if json.Unmarshal(e.Message, &m) == nil {
				role, text, messageID = e.Type, geminiText(m.Parts), e.UUID
			}
		}
	case "workbuddy":
		switch e.Type {
		case "message", "session-meta", "ai-title", "custom-title":
			r.id, r.cwd = e.SessionID, e.Cwd
		default:
			return record{}, true
		}
		switch e.Type {
		case "message":
			role, text, messageID = e.Role, contentText(e.Content), e.ID
			if e.Role == "user" {
				text = filteredContentText(e.Content, workBuddyVisible)
			}
		case "ai-title":
			r.title = e.AITitle
		case "custom-title":
			r.title = e.CustomTitle
		}
	case "copilot":
		var data struct {
			ID        string `json:"sessionId"`
			MessageID string `json:"messageId"`
			Content   string `json:"content"`
			Title     string `json:"title"`
			Summary   string `json:"summary"`
			StartTime string `json:"startTime"`
			Cwd       string `json:"cwd"`
			Context   struct {
				Cwd string `json:"cwd"`
			} `json:"context"`
			Autopilot bool `json:"isAutopilotContinuation"`
		}
		if len(e.Data) != 0 && json.Unmarshal(e.Data, &data) != nil {
			return record{}, false
		}
		switch e.Type {
		case "session.start":
			r.kind, r.id, r.cwd = "session", data.ID, firstNonEmpty(data.Context.Cwd, data.Cwd)
			r.started, _ = time.Parse(time.RFC3339Nano, data.StartTime)
		case "session.title_changed":
			r.kind, r.title = "custom-title", data.Title
		case "session.info":
			r.title = data.Summary
		case "user.message":
			if !data.Autopilot {
				role, text, messageID = "user", data.Content, firstNonEmpty(data.MessageID, e.ID)
			}
		case "assistant.message":
			role, text, messageID = "assistant", data.Content, firstNonEmpty(data.MessageID, e.ID)
		}
	case "pi":
		r.id, r.parent = e.ID, e.ParentID
		switch e.Type {
		case "session":
			r.cwd, r.parent, r.started = e.Cwd, e.ParentSession, r.at
		case "session_info":
			r.title = e.Name
		case "message":
			var m utterance
			if json.Unmarshal(e.Message, &m) == nil {
				role, text, messageID = m.Role, contentText(m.Content), e.ID
				if r.at.IsZero() {
					r.at = parseTime(m.Timestamp)
				}
			}
		}
	}
	if (role == "user" || role == "assistant") && strings.TrimSpace(text) != "" {
		r.message = Message{ID: messageID, Role: role, Text: text, Timestamp: formatTime(r.at), Line: number}
	}
	if r.at.IsZero() && r.started.IsZero() && r.id == "" && r.cwd == "" && r.title == "" && r.message.Line == 0 &&
		e.Type != "session" && e.Type != "session_meta" {
		return record{}, true
	}
	return r, true
}

func contentText(raw json.RawMessage) string {
	return filteredContentText(raw, nil)
}

func filteredContentText(raw json.RawMessage, keep func(string) bool) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if keep != nil && !keep(text) {
			return ""
		}
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var words []string
	for _, p := range parts {
		switch p.Type {
		case "text", "input_text", "output_text":
			if p.Text != "" && (keep == nil || keep(p.Text)) {
				words = append(words, p.Text)
			}
		}
	}
	return strings.Join(words, "\n")
}

func stripCodexContext(text string) string {
	text = strings.TrimSpace(text)
	for {
		body := text
		var closeTag string
		switch {
		case strings.HasPrefix(text, "# AGENTS.md instructions for "):
			_, afterHeading, _ := strings.Cut(text, "\n")
			body = strings.TrimSpace(afterHeading)
			if !strings.HasPrefix(body, "<INSTRUCTIONS>") {
				return text
			}
			closeTag = "</INSTRUCTIONS>"
		case strings.HasPrefix(text, "<environment_context>"):
			closeTag = "</environment_context>"
		case strings.HasPrefix(text, "<user_instructions>"):
			closeTag = "</user_instructions>"
		case strings.HasPrefix(text, "<permissions instructions>"):
			closeTag = "</permissions instructions>"
		default:
			return text
		}
		_, remaining, found := strings.Cut(body, closeTag)
		if !found {
			return text
		}
		text = strings.TrimSpace(remaining)
	}
}

func parseTime(raw json.RawMessage) time.Time {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		stamp, _ := time.Parse(time.RFC3339Nano, value)
		return stamp
	}
	if milliseconds, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return time.UnixMilli(milliseconds)
	}
	return time.Time{}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (src source) buildSession(rows []record, modified time.Time) candidate {
	s := Session{Agent: src.agent, Path: src.path, Messages: []Message{}}
	var start, updated, headerTime time.Time
	var nativeID, parent string
	var recognized bool
	var titlePriority int
	for _, row := range rows {
		header := row.kind == "session" || row.kind == "session_meta"
		hasSessionID := src.agent == "claude" || src.agent == "qwen" || src.agent == "workbuddy"
		if header || hasSessionID {
			nativeID = firstNonEmpty(nativeID, row.id)
			parent = firstNonEmpty(parent, row.parent)
		}
		if header {
			headerTime = row.started
			if headerTime.IsZero() {
				headerTime = row.at
			}
			s.Cwd = firstNonEmpty(row.cwd, s.Cwd)
		} else {
			s.Cwd = firstNonEmpty(s.Cwd, row.cwd)
		}
		if !row.at.IsZero() && (start.IsZero() || row.at.Before(start)) {
			start = row.at
		}
		if row.at.After(updated) {
			updated = row.at
		}
		if row.title != "" {
			var priority int
			switch row.kind {
			case "custom-title":
				priority = 2
			case "ai-title":
				priority = 1
			}
			if priority >= titlePriority {
				s.Title, titlePriority = row.title, priority
			}
		}
		recognized = recognized || header || row.message.Line != 0 || row.id != "" && hasSessionID
	}
	if !recognized || src.agent == "dsh" && nativeID == "" {
		return candidate{}
	}
	if src.agent == "copilot" && nativeID == "" {
		nativeID = filepath.Base(filepath.Dir(src.path))
	}
	if src.agent == "claude" {
		if owner, child := claudeSubagent(src.path); owner != "" {
			parent, nativeID = owner, owner+"/"+child
		}
	}
	if src.agent == "workbuddy" {
		name := strings.TrimSuffix(filepath.Base(src.path), filepath.Ext(src.path))
		if dir := filepath.Dir(src.path); filepath.Base(dir) == "subagents" {
			parent = filepath.Base(filepath.Dir(dir))
			nativeID = parent + "/" + name
		} else {
			nativeID = firstNonEmpty(nativeID, name)
		}
	}
	if nativeID == "" {
		nativeID = src.fallbackID()
	}
	s.ID = src.agent + ":" + nativeID
	if src.agent != "pi" && parent != "" && parent != nativeID {
		s.ParentID = src.agent + ":" + parent
	}
	if !headerTime.IsZero() {
		start = headerTime
	}
	if start.IsZero() {
		start = modified
	}
	if updated.Before(start) {
		updated = start
	}
	s.Start, s.Updated = formatTime(start), formatTime(updated)

	if src.agent == "pi" {
		rows = piBranch(rows)
	}
	var lastKind string
	var paired bool
	nativeMessages := make(map[string]Message)
	for _, row := range rows {
		m := row.message
		if m.Line == 0 {
			continue
		}
		if src.agent == "codex" && !paired && len(s.Messages) != 0 && lastKind != row.kind {
			last := s.Messages[len(s.Messages)-1]
			if last.Role == m.Role && strings.TrimSpace(last.Text) == strings.TrimSpace(m.Text) {
				paired = true
				continue
			}
		}
		lastKind, paired = row.kind, false
		// Physical positions stay stable when a writer appends records or
		// when a Codex rollout moves to archived_sessions.
		if m.ID == "" || src.agent == "codex" {
			m.ID = s.ID + ":line:" + strconv.Itoa(m.Line)
		} else {
			m.ID = s.ID + ":message:" + m.ID
			if previous, exists := nativeMessages[m.ID]; exists {
				if previous.Role == m.Role && previous.Text == m.Text && previous.Timestamp == m.Timestamp {
					continue
				}
				m.ID += ":line:" + strconv.Itoa(m.Line)
			}
			nativeMessages[m.ID] = m
		}
		s.Messages = append(s.Messages, m)
		if s.Title == "" && m.Role == "user" {
			s.Title = displayTitle(m.Text)
		}
	}
	if s.Title == "" {
		name := filepath.Base(src.path)
		if src.compressed() {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		s.Title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	return candidate{session: s, updated: updated, parentRef: parent}
}

func piBranch(rows []record) []record {
	byID := make(map[string]record)
	var leaf string
	for _, row := range rows {
		if row.kind != "session" && row.id != "" {
			byID[row.id], leaf = row, row.id
		}
	}
	if leaf == "" {
		// Version 1 stored a linear transcript without entry IDs.
		return rows
	}
	var branch []record
	visited := make(map[string]bool)
	for leaf != "" && !visited[leaf] {
		row, ok := byID[leaf]
		if !ok {
			break
		}
		visited[leaf] = true
		branch = append(branch, row)
		leaf = row.parent
	}
	slices.Reverse(branch)
	return branch
}

func claudeSubagent(path string) (parent, child string) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if !strings.HasPrefix(name, "agent-") {
		return "", ""
	}
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "subagents" {
			return filepath.Base(filepath.Dir(dir)), name
		}
	}
	return "", ""
}

func claudeParentID(path, owner string) string {
	meta, err := os.Open(strings.TrimSuffix(path, filepath.Ext(path)) + ".meta.json")
	if err != nil {
		return owner
	}
	defer meta.Close()
	var info struct {
		ParentAgentID string `json:"parentAgentId"`
	}
	if err := json.NewDecoder(io.LimitReader(meta, 64<<10)).Decode(&info); err != nil || info.ParentAgentID == "" {
		return owner
	}
	return owner + "/agent-" + info.ParentAgentID
}

func (src source) fallbackID() string {
	name := filepath.Base(src.path)
	key := src.path
	if src.agent == "codex" && strings.HasPrefix(name, "rollout-") {
		key = name
	}
	if src.compressed() {
		key = strings.TrimSuffix(key, filepath.Ext(key))
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("file-%x", sum[:16])
}

func displayTitle(text string) string {
	title, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	title = strings.TrimSpace(title)
	n := 0
	for offset := range title {
		if n == 96 {
			return title[:offset] + "…"
		}
		n++
	}
	return title
}
