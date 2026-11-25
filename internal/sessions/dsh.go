package sessions

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A migrated session may retain both the prior generation and a compressed twin.
var dshFilename = regexp.MustCompile(`^session(?:\.v([0-9]+))?\.jsonl(?:\.zstd)?$`)

func dshGeneration(name string) (int, bool) {
	match := dshFilename.FindStringSubmatch(name)
	if match == nil {
		return 0, false
	}
	if match[1] == "" {
		return 0, true
	}
	generation, err := strconv.Atoi(match[1])
	return generation, err == nil
}

type dshFile struct {
	discoveredSource
	generation int
	modified   time.Time
}

func (f dshFile) preferredTo(other dshFile) bool {
	if f.generation != other.generation {
		return f.generation > other.generation
	}
	if !f.modified.Equal(other.modified) {
		return f.modified.After(other.modified)
	}
	return !f.compressed() && other.compressed()
}

func parseDSH(line []byte, number int) (record, bool) {
	var event struct {
		Type            string          `json:"type"`
		ID              string          `json:"id"`
		Cwd             string          `json:"cwd"`
		Parent          string          `json:"parentSession"`
		DelegationDepth int             `json:"delegationDepth"`
		Created         json.RawMessage `json:"createdAt"`
		Time            json.RawMessage `json:"time"`
		Data            struct {
			ID      string          `json:"id"`
			Content json.RawMessage `json:"content"`
			Title   string          `json:"title"`
			Source  struct{ Kind string }
			Message struct {
				ID      string          `json:"id"`
				Content json.RawMessage `json:"content"`
			}
		}
	}
	if json.Unmarshal(line, &event) != nil {
		return record{}, false
	}
	r := record{kind: event.Type, at: parseTime(event.Time)}
	var role, text, id string
	switch event.Type {
	case "session":
		if event.ID == "" || number != 1 {
			return record{}, false
		}
		r.id, r.cwd, r.started = event.ID, event.Cwd, parseTime(event.Created)
		if event.DelegationDepth > 0 {
			r.parent = event.Parent
		}
	case "session/title":
		r.title = event.Data.Title
	case "user/message":
		if event.Data.Source.Kind != "user" {
			return record{}, true
		}
		role, text, id = "user", contentText(event.Data.Content), event.Data.ID
	case "assistant/message":
		role, text, id = "assistant", contentText(event.Data.Message.Content), event.Data.Message.ID
	default:
		return record{}, true
	}
	if role != "" && strings.TrimSpace(text) != "" {
		r.message = Message{ID: id, Role: role, Text: text, Timestamp: formatTime(r.at), Line: number}
	}
	return r, true
}
