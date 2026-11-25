package studio

import (
	"context"
	"net/http"
	"strings"

	"github.com/xiejun23/Knooo/internal/sessions"
)

func (a *app) archivedSessions(ctx context.Context) (map[string]bool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT session FROM archived_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (a *app) setHistoryArchive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs      []string `json:"ids"`
		Archived *bool    `json:"archived"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || body.Archived == nil {
		http.Error(w, "请选择会话和归档操作", http.StatusBadRequest)
		return
	}
	a.ensureSessionIndex()
	known := make(map[string]bool)
	for _, session := range a.sessionsSnapshot() {
		known[session.ID] = true
	}
	for _, id := range body.IDs {
		if !known[id] {
			http.Error(w, "所选会话已不在索引中，请刷新后重试", http.StatusConflict)
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err == nil {
		defer tx.Rollback()
		statement := `DELETE FROM archived_sessions WHERE session=?`
		if *body.Archived {
			statement = `INSERT INTO archived_sessions(session) VALUES(?) ON CONFLICT DO NOTHING`
		}
		for _, id := range body.IDs {
			if _, err = tx.ExecContext(ctx, statement, id); err != nil {
				break
			}
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		http.Error(w, "无法保存会话归档", http.StatusInternalServerError)
		return
	}
	a.wakeReview(true)
	jsonReply(w, map[string]bool{"ok": true})
}

func (a *app) searchHistory(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	query := strings.ToLower(strings.TrimSpace(args.Get("q")))
	result := make(map[string]match)
	if query == "" {
		jsonReply(w, result)
		return
	}
	a.ensureSessionIndex()
	for _, session := range a.sessionsSnapshot() {
		if r.Context().Err() != nil {
			return
		}
		if !session.MatchesAgent(args["agent"]...) {
			continue
		}
		for _, m := range session.Messages {
			if r.Context().Err() != nil {
				return
			}
			if snippet := textSnippet(m.Text, query); snippet != "" {
				result[session.ID] = match{SessionID: session.ID, MessageID: m.ID, Snippet: snippet}
				break
			}
		}
		if _, found := result[session.ID]; !found && matchesSessionMetadata(session, query) {
			result[session.ID] = match{SessionID: session.ID}
		}
	}
	jsonReply(w, result)
}

func projectPath(s sessions.Session) string {
	if s.ProjectDisplayPath != "" {
		return s.ProjectDisplayPath
	}
	return s.ProjectPath
}

func sessionLocation(s sessions.Session) string {
	if s.WSL != "" {
		return "wsl:" + s.WSL
	}
	return s.Platform
}

func inProject(s sessions.Session, path, location string) bool {
	return (path == "" || path == s.ProjectPath || path == projectPath(s)) &&
		(location == "" || location == sessionLocation(s))
}

func matchesSessionMetadata(s sessions.Session, query string) bool {
	for _, value := range []string{s.Title, s.Project, s.ID, s.Platform, s.WSL} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	pathQuery := strings.ReplaceAll(query, `\`, "/")
	for _, value := range []string{s.ProjectPath, s.ProjectDisplayPath, s.Cwd, s.Path, s.SourceRoot} {
		if strings.Contains(strings.ToLower(strings.ReplaceAll(value, `\`, "/")), pathQuery) {
			return true
		}
	}
	return false
}
