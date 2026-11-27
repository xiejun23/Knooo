package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestWorkspaceBackupTransfersSavedWorkAcrossDatabases(t *testing.T) {
	item := reviewTestSession()
	source := openReviewTestApp(t, t.TempDir(), item)
	target := openReviewTestApp(t, t.TempDir(), item)
	topic := stampTopicEvidence([]distill.Topic{{
		Title: "Saved source topic", Status: "verified", Evidence: []string{item.Messages[0].ID},
	}}, item.Messages)[0]
	rawTopic := backupTestJSON(t, topic)
	rawTopics := backupTestJSON(t, []distill.Topic{topic})

	reviewTestRequest(t, source, http.MethodPost, "/api/history/archive",
		backupTestJSON(t, map[string]any{"ids": []string{item.ID}, "archived": true}))
	reviewTestRequest(t, source, http.MethodPost, "/api/notes", backupTestJSON(t, note{SessionID: item.ID, Topic: topic}))
	backupTestExec(t, source, `INSERT INTO topics VALUES(?,?),('legacy-topic',?)`, item.ID, rawTopics, rawTopics)

	ctx := t.Context()
	if _, err := source.saveReview(ctx, item.ID, reviewRevision(item.Messages), reviewCheckpoint{
		Consumed: len(item.Messages), Topics: []distill.Topic{topic}, Retry: &reviewRetry{Token: "private-retry-token"},
	}, storedReview{}); err != nil {
		t.Fatal(err)
	}
	backupTestExec(t, source, `INSERT INTO reviews VALUES('legacy-review',?,?)`, reviewRevision(item.Messages), rawTopics)
	backupTestExec(t, source, `INSERT INTO reviews VALUES('queued-review',?,?)`, reviewRevision(nil),
		backupTestJSON(t, reviewCheckpoint{Retry: &reviewRetry{Token: "queued-private-retry"}}))
	backupTestExec(t, source, `INSERT INTO settings VALUES('machine-setting','private-machine-setting')`)
	backupTestExec(t, source, `INSERT INTO model_keys VALUES('connection',X'01234567')`)
	backupTestExec(t, source, `INSERT INTO review_failures VALUES('failure','r',1,'private-review-failure')`)
	source.recordEvent(platformEvent{Area: "model", Level: "info", Action: "private-platform-log"})
	source.sessions = append(source.sessions, sessions.Session{ID: "unrelated-private-session", Messages: []sessions.Message{{Text: "raw-private-message"}}})

	reportPath := "/api/report/document?" + reviewTestWindow
	query, err := url.ParseQuery(reviewTestWindow)
	if err != nil {
		t.Fatal(err)
	}
	from, to, err := reviewWindow(query)
	if err != nil {
		t.Fatal(err)
	}
	scope := reportDocumentScope(query, from, to)
	report := workReport{Title: "Generated report", Mode: "local", Sections: []reportSection{}, Markdown: "# Generated report"}
	draft := report
	draft.Title = "Saved edited report"
	if err := source.storeGeneratedReport(ctx, scope, report); err != nil {
		t.Fatal(err)
	}
	reviewTestRequest(t, source, http.MethodPost, reportPath, backupTestJSON(t, map[string]any{
		"draft": draft, "draftRevision": 0, "reportRevision": 1,
	}))

	backupTestCall(t, source, http.MethodGet, "", "", http.StatusForbidden)
	backupTestCall(t, target, http.MethodPost, "other-origin", "", http.StatusForbidden)
	data := backupTestCall(t, source, http.MethodGet, "local", "", http.StatusOK)
	var backup workspaceBackup
	if err := json.Unmarshal([]byte(data), &backup); err != nil {
		t.Fatal(err)
	}
	if backup.Version != workspaceBackupVersion || len(backup.Topics) != 2 || len(backup.Notes) != 1 ||
		!slices.Equal(backup.ArchivedSessions, []string{item.ID}) || len(backup.Reviews) != 2 || len(backup.Reports) != 1 {
		t.Fatalf("unexpected backup contents: %s", data)
	}
	for _, omitted := range []string{"private-retry-token", "queued-private-retry", "private-machine-setting", "model_keys", "01234567",
		"private-review-failure", "private-platform-log", "raw-private-message", "unrelated-private-session"} {
		if strings.Contains(data, omitted) {
			t.Errorf("backup contains excluded data %q", omitted)
		}
	}
	conflict := topic
	conflict.Title = "Existing target topic"
	backupTestExec(t, target, `INSERT INTO topics VALUES(?,?)`, item.ID, backupTestJSON(t, []distill.Topic{conflict}))
	backupTestExec(t, target, `INSERT INTO notes(id, session, content) VALUES(99,'already-present',?)`, rawTopic)

	if actual := backupTestCall(t, target, http.MethodPost, "local", data, http.StatusOK); actual != "{\"added\":6,\"skipped\":1}\n" {
		t.Fatalf("first restore: %s", actual)
	}
	if actual := backupTestCall(t, target, http.MethodPost, "local", data, http.StatusOK); actual != "{\"added\":0,\"skipped\":7}\n" {
		t.Fatalf("repeated restore: %s", actual)
	}

	notes := reviewTestRequest(t, target, http.MethodGet, "/api/notes", "")
	if !strings.Contains(notes, "\"id\":100") || !strings.Contains(notes, topic.EvidenceVersions[item.Messages[0].ID]) {
		t.Fatalf("restored note lost its evidence or failed to get a new ID: %s", notes)
	}
	detail := reviewTestRequest(t, target, http.MethodGet, "/api/session?id="+url.QueryEscape(item.ID), "")
	if !strings.Contains(detail, conflict.Title) {
		t.Fatalf("restore replaced the existing topic: %s", detail)
	}
	read := reviewTestRequest(t, target, http.MethodGet, reportPath, "")
	if !strings.Contains(read, draft.Title) || !strings.Contains(read, "\"draftRevision\":1") {
		t.Fatalf("restored report draft missing or reset: %s", read)
	}
	reviews, err := target.readReviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current, valid := reviews[item.ID].verified(item.Messages, reviewRevision(item.Messages)); !valid ||
		current.Retry != nil || current.Consumed != len(item.Messages) || len(current.Topics) != 1 ||
		reviews["legacy-review"].Consumed != -1 {
		t.Fatalf("saved review state did not round-trip: %+v", reviews)
	}
	for _, query := range []string{"SELECT count(*) FROM settings", "SELECT count(*) FROM model_keys",
		"SELECT count(*) FROM review_failures", "SELECT count(*) FROM platform_logs"} {
		var rows int
		if err := target.db.QueryRowContext(ctx, query).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("restore populated excluded data: %s, count=%d, error=%v", query, rows, err)
		}
	}
}

func TestWorkspaceBackupRejectsInvalidInputAndRollsBackFailedWrite(t *testing.T) {
	target := openReviewTestApp(t, t.TempDir(), reviewTestSession())
	for _, body := range []string{
		`null`,
		`{"version":2}`,
		`{"version":1} {}`,
		`{"version":1,"modelKeys":[]}`,
		`{"version":1,"archivedSessions":["accepted",""]}`,
		`{"version":1,"archivedSessions":["accepted"],"reports":[{"scope":"invalid"}]}`,
	} {
		backupTestCall(t, target, http.MethodPost, "local", body, http.StatusBadRequest)
	}
	backupTestExec(t, target, `CREATE TRIGGER reject_archived_probe BEFORE INSERT ON archived_sessions WHEN NEW.session = 'reject'
		BEGIN SELECT RAISE(ABORT, 'force rollback'); END`)
	backupTestCall(t, target, http.MethodPost, "local",
		`{"version":1,"archivedSessions":["accepted","reject"]}`, http.StatusInternalServerError)

	var rows int
	if err := target.db.QueryRowContext(t.Context(), `SELECT count(*) FROM archived_sessions`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("restore must not write partial data after an error: count=%d, error=%v", rows, err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43170/api/backup", strings.NewReader("{}"))
	req.ContentLength = maxWorkspaceBackupBytes + 1
	req.Header.Set("Authorization", "Bearer test-token")
	res := httptest.NewRecorder()
	target.Handler(req.Host, "test-token", http.NotFoundHandler(), "", "", nil, nil).ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("restore accepted an oversized backup: HTTP %d, %s", res.Code, res.Body)
	}
}

func backupTestExec(t *testing.T, a *app, query string, args ...any) {
	t.Helper()
	if _, err := a.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func backupTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func backupTestCall(t *testing.T, a *app, method, origin, body string, status int) string {
	t.Helper()
	const address = "127.0.0.1:43170"
	req := httptest.NewRequest(method, "http://"+address+"/api/backup", strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Authorization", "Bearer test-token")
	}
	if origin == "other-origin" {
		req.Header.Set("Origin", "http://localhost:1")
	}
	w := httptest.NewRecorder()
	a.Handler(address, "test-token", http.NotFoundHandler(), "", "", nil, nil).ServeHTTP(w, req)
	if w.Code != status {
		t.Fatalf("%s /api/backup: HTTP %d, want %d: %s", method, w.Code, status, w.Body)
	}
	return w.Body.String()
}
