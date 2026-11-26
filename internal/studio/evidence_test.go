package studio

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestTopicEvidenceSurvivesAppendAndDetectsSourceChanges(t *testing.T) {
	a, err := New(nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions = []sessions.Session{{ID: "fixture", Title: "Fixture", Messages: []sessions.Message{{ID: "line-1", Role: "user", Text: "original"}}}}
	raw := reviewTestRequest(t, a, http.MethodPost, "/api/distill", `{"id":"fixture"}`)
	var topics []distill.Topic
	if err := json.Unmarshal([]byte(raw), &topics); err != nil || len(topics) != 1 || topics[0].EvidenceVersions["line-1"] == "" {
		t.Fatalf("missing stored source version: %s (%v)", raw, err)
	}
	data, err := json.Marshal(note{SessionID: "fixture", Topic: topics[0]})
	if err != nil {
		t.Fatal(err)
	}
	reviewTestRequest(t, a, http.MethodPost, "/api/notes", string(data))
	if _, err := a.db.Exec(`INSERT INTO topics VALUES('unrelated','invalid JSON')`); err != nil {
		t.Fatal(err)
	}
	a.sessions[0].Messages = append(a.sessions[0].Messages, sessions.Message{ID: "line-2", Text: "new message"})
	assertDetail := func(stale bool) {
		t.Helper()
		version := topics[0].EvidenceVersions["line-1"]
		raw := reviewTestRequest(t, a, http.MethodGet, "/api/session?id=fixture&anchor=line-1&evidenceVersion="+version, "")
		var response struct {
			Topics      []distill.Topic
			StaleAnchor bool
		}
		if err := json.Unmarshal([]byte(raw), &response); err != nil || len(response.Topics) != 1 ||
			response.Topics[0].EvidenceStale != stale || response.StaleAnchor != stale {
			t.Fatalf("expected stale=%v, got %s (%v)", stale, raw, err)
		}
	}
	assertDetail(false)
	a.sessions[0].Messages[0].Text = "rewritten at the same position"
	assertDetail(true)
	raw = reviewTestRequest(t, a, http.MethodGet, "/api/notes", "")
	var notes []note
	if err := json.Unmarshal([]byte(raw), &notes); err != nil || len(notes) != 1 || !notes[0].Topic.EvidenceStale {
		t.Fatalf("expected preserved note with stale evidence: %s (%v)", raw, err)
	}
}
