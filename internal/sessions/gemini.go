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
	"strings"
	"time"
)

func (src source) isTranscript() bool {
	ext := strings.ToLower(filepath.Ext(src.path))
	switch src.agent {
	case "antigravity":
		return ext == ".db" && antigravityDBName.MatchString(filepath.Base(src.path))
	case "copilot":
		return filepath.Base(src.path) == "events.jsonl"
	case "qwen":
		return ext == ".jsonl" && slices.Contains(strings.Split(filepath.ToSlash(src.path), "/"), "chats")
	case "gemini":
		if !slices.Contains(strings.Split(filepath.ToSlash(src.path), "/"), "chats") {
			return false
		}
		return ext == ".jsonl" || ext == ".json" && strings.HasPrefix(filepath.Base(src.path), "session-")
	default:
		return ext == ".jsonl" || src.compressed()
	}
}

func geminiProjectDir(path string) string {
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "chats" {
			return filepath.Dir(dir)
		}
	}
	return ""
}

func geminiProjectPaths(root string) map[string]string {
	var registry struct{ Projects map[string]string }
	if raw, err := os.ReadFile(filepath.Join(root, "..", "projects.json")); err == nil {
		_ = json.Unmarshal(raw, &registry)
	}
	paths := make(map[string]string)
	for cwd, slug := range registry.Projects {
		paths[filepath.Join(root, slug)] = cwd
		paths[filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(cwd))))] = cwd
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if raw, err := os.ReadFile(filepath.Join(dir, ".project_root")); err == nil {
			path := strings.TrimSpace(string(raw))
			if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
				paths[dir] = path
			}
		}
	}
	return paths
}

type geminiPatch struct {
	ID      string
	Content json.RawMessage
}

type geminiChange struct {
	Rewind string `json:"$rewindTo"`
	Patch  *struct {
		geminiPatch
		Updates   []geminiPatch
		RemoveIDs []string
		OrderIDs  []string
	} `json:"$patch"`
	Set *struct {
		geminiMetadata
		Messages *[]geminiMessage
	} `json:"$set"`
}

type geminiMetadata struct {
	SessionID, StartTime, LastUpdated, Summary string
}

func (m geminiMetadata) record() record {
	started, _ := time.Parse(time.RFC3339Nano, m.StartTime)
	updated, _ := time.Parse(time.RFC3339Nano, m.LastUpdated)
	kind := "metadata"
	if m.SessionID != "" {
		kind = "session"
	}
	return record{kind: kind, id: m.SessionID, title: m.Summary, at: updated, started: started}
}

type geminiMessage struct {
	ID, Type  string
	Timestamp json.RawMessage
	Content   json.RawMessage
}

func (m geminiMessage) record(line int) record {
	if m.Type == "user" || m.Type == "gemini" {
		role := "user"
		if m.Type == "gemini" {
			role = "assistant"
		}
		at := parseTime(m.Timestamp)
		return record{kind: "message", at: at, message: Message{ID: m.ID, Role: role, Text: geminiText(m.Content), Timestamp: formatTime(at), Line: line}}
	}
	// Non-conversational rows can still serve as rewind or reorder anchors.
	if m.ID != "" && m.Type != "" {
		return record{kind: "message", message: Message{ID: m.ID}}
	}
	return record{}
}

func parseGemini(line []byte, number int) (record, bool) {
	var row struct {
		geminiChange
		geminiMetadata
		geminiMessage
	}
	if json.Unmarshal(line, &row) != nil {
		return record{}, false
	}
	if row.Patch != nil || row.Rewind != "" || row.Set != nil {
		return record{kind: "gemini-change", gemini: &row.geminiChange, message: Message{Line: number}}, true
	}
	if row.SessionID != "" || row.Summary != "" || row.LastUpdated != "" {
		return row.geminiMetadata.record(), true
	}
	return row.geminiMessage.record(number), true
}

func geminiText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		var text []string
		for _, part := range parts {
			if value := geminiText(part); value != "" {
				text = append(text, value)
			}
		}
		return strings.Join(text, "\n")
	}
	var part struct {
		Text    string
		Thought bool
	}
	if json.Unmarshal(raw, &part) == nil && !part.Thought {
		return part.Text
	}
	return ""
}

func geminiMessages(rows []record) []record {
	var result []record
	var order []string
	messages := make(map[string]record)
	apply := func(edit geminiPatch) {
		if row, ok := messages[edit.ID]; ok && edit.Content != nil {
			row.message.Text = geminiText(edit.Content)
			messages[edit.ID] = row
		}
	}
	add := func(row record) {
		id := row.message.ID
		if id == "" {
			id = fmt.Sprintf("line:%d", row.message.Line)
		}
		if _, exists := messages[id]; !exists {
			order = append(order, id)
		}
		messages[id] = row
	}
	for _, row := range rows {
		switch {
		case row.gemini != nil && row.gemini.Rewind != "":
			index := max(slices.Index(order, row.gemini.Rewind), 0)
			for _, id := range order[index:] {
				delete(messages, id)
			}
			order = order[:index]
		case row.gemini != nil && row.gemini.Set != nil:
			set := row.gemini.Set
			result = append(result, set.geminiMetadata.record())
			if set.Messages != nil {
				clear(messages)
				order = order[:0]
				for _, message := range *set.Messages {
					if message.ID != "" {
						add(message.record(row.message.Line))
					}
				}
			}
		case row.gemini != nil:
			patch := row.gemini.Patch
			apply(patch.geminiPatch)
			for _, edit := range patch.Updates {
				apply(edit)
			}
			for _, id := range patch.RemoveIDs {
				delete(messages, id)
			}
			order = slices.DeleteFunc(order, func(id string) bool {
				_, exists := messages[id]
				return !exists || slices.Contains(patch.OrderIDs, id)
			})
			for _, id := range patch.OrderIDs {
				if _, exists := messages[id]; exists && !slices.Contains(order, id) {
					order = append(order, id)
				}
			}
		case row.kind == "message":
			add(row)
		default:
			result = append(result, row)
		}
	}
	for _, id := range order {
		if row := messages[id]; strings.TrimSpace(row.message.Text) != "" && row.message.Line > 0 {
			result = append(result, row)
		}
	}
	return result
}

func geminiLegacy(file io.Reader) ([]record, error) {
	raw, err := io.ReadAll(io.LimitReader(file, maxLineBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxLineBytes {
		return nil, fmt.Errorf("Gemini JSON exceeds %d bytes", maxLineBytes)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var body struct{ Messages []json.RawMessage }
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	header, ok := parseGemini(raw, 1)
	if !ok || header.kind != "session" {
		return nil, fmt.Errorf("missing Gemini session metadata")
	}
	rows := []record{header}
	line, offset := 1, 0
	for _, msg := range body.Messages {
		pos := bytes.Index(raw[offset:], msg)
		if pos < 0 {
			return nil, fmt.Errorf("unable to locate Gemini message")
		}
		line += bytes.Count(raw[offset:offset+pos], []byte{'\n'})
		row, _ := parseGemini(msg, line)
		rows = append(rows, row)
		line += bytes.Count(msg, []byte{'\n'})
		offset += pos + len(msg)
	}
	return rows, nil
}
