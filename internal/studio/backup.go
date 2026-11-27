package studio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/xiejun23/Knooo/internal/distill"
)

const (
	workspaceBackupVersion  = 1
	maxWorkspaceBackupBytes = 64 << 20
)

type workspaceBackup struct {
	Version          int                                     `json:"version"`
	Topics           []backupSessionContent[[]distill.Topic] `json:"topics"`
	Notes            []backupSessionContent[distill.Topic]   `json:"notes"`
	ArchivedSessions []string                                `json:"archivedSessions"`
	Reviews          []backupReview                          `json:"reviews"`
	Reports          []backupReport                          `json:"reports"`
}

type backupSessionContent[T any] struct {
	SessionID string `json:"sessionId"`
	Content   T      `json:"content"`
}

type backupReview struct {
	SessionID string          `json:"sessionId"`
	Revision  string          `json:"revision"`
	Consumed  int             `json:"consumed"`
	Topics    []distill.Topic `json:"topics"`
}

type backupReport struct {
	Scope          string      `json:"scope"`
	Report         workReport  `json:"report"`
	ReportRevision int64       `json:"reportRevision"`
	Draft          *workReport `json:"draft"`
	DraftRevision  int64       `json:"draftRevision"`
}

type backupImportResult struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
}

func (a *app) exportBackup(w http.ResponseWriter, r *http.Request) {
	if a.demo {
		http.Error(w, "演示工作区不支持备份", http.StatusConflict)
		return
	}
	backup, err := a.readBackup(r.Context())
	if err != nil || !backup.valid() {
		http.Error(w, "无法读取本机成果，请检查工作区后重试", http.StatusInternalServerError)
		return
	}
	data, err := json.Marshal(backup)
	if err != nil {
		http.Error(w, "无法生成本机成果备份", http.StatusInternalServerError)
		return
	}
	// shortcut: one in-memory JSON file, use streaming if workspaces routinely exceed 64 MiB.
	if len(data) > maxWorkspaceBackupBytes {
		http.Error(w, "备份超过 64 MiB", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *app) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if a.demo {
		http.Error(w, "演示工作区不支持恢复备份", http.StatusConflict)
		return
	}
	if r.ContentLength > maxWorkspaceBackupBytes {
		http.Error(w, "备份超过 64 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	var backup *workspaceBackup
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWorkspaceBackupBytes))
	d.DisallowUnknownFields()
	err := d.Decode(&backup)
	if err == nil {
		if tail := d.Decode(new(any)); tail != io.EOF {
			err = tail
			if err == nil {
				err = errors.New("unexpected backup data after JSON object")
			}
		}
	}
	var sizeError *http.MaxBytesError
	switch {
	case errors.As(err, &sizeError):
		http.Error(w, "备份超过 64 MiB", http.StatusRequestEntityTooLarge)
		return
	case err != nil || backup == nil:
		http.Error(w, "备份文件格式无效", http.StatusBadRequest)
		return
	case backup.Version != workspaceBackupVersion:
		http.Error(w, "不支持此备份文件版本", http.StatusBadRequest)
		return
	case !backup.valid():
		http.Error(w, "备份文件包含无效的工作区记录", http.StatusBadRequest)
		return
	}
	result, err := a.importBackup(r.Context(), *backup)
	if err != nil {
		http.Error(w, "无法恢复本机成果，请重试", http.StatusInternalServerError)
		return
	}
	if result.Added > 0 && len(backup.ArchivedSessions) > 0 {
		a.wakeReview(true)
	}
	jsonReply(w, result)
}

func (a *app) readBackup(ctx context.Context) (workspaceBackup, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return workspaceBackup{}, err
	}
	defer tx.Rollback()
	backup := workspaceBackup{Version: workspaceBackupVersion}
	backup.Topics, err = readBackupRows(ctx, tx, `SELECT session, content FROM topics ORDER BY session`, readBackupContent[[]distill.Topic])
	if err != nil {
		return backup, err
	}
	backup.Notes, err = readBackupRows(ctx, tx, `SELECT session, content FROM notes ORDER BY id`, readBackupContent[distill.Topic])
	if err != nil {
		return backup, err
	}
	backup.ArchivedSessions, err = readBackupRows(ctx, tx, `SELECT session FROM archived_sessions ORDER BY session`, func(rows *sql.Rows) (string, error) {
		var id string
		err := rows.Scan(&id)
		return id, err
	})
	if err != nil {
		return backup, err
	}
	backup.Reviews, err = readBackupRows(ctx, tx, `SELECT session, revision, content FROM reviews ORDER BY session`, readBackupReview)
	if err != nil {
		return backup, err
	}
	backup.Reviews = slices.DeleteFunc(backup.Reviews, func(row backupReview) bool {
		// A queued full retry can have a placeholder without any saved progress.
		return row.Consumed == 0 && len(row.Topics) == 0
	})
	backup.Reports, err = readBackupRows(ctx, tx, `SELECT scope, report, report_revision, draft, draft_revision FROM report_documents ORDER BY scope`, readBackupReport)
	if err != nil {
		return backup, err
	}
	return backup, tx.Commit()
}

func readBackupRows[T any](ctx context.Context, tx *sql.Tx, query string, read func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []T{}
	for rows.Next() {
		row, err := read(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func readBackupContent[T any](rows *sql.Rows) (backupSessionContent[T], error) {
	var row backupSessionContent[T]
	var content string
	if err := rows.Scan(&row.SessionID, &content); err != nil {
		return row, err
	}
	err := json.Unmarshal([]byte(content), &row.Content)
	return row, err
}

func readBackupReview(rows *sql.Rows) (backupReview, error) {
	var row backupReview
	var raw string
	if err := rows.Scan(&row.SessionID, &row.Revision, &raw); err != nil {
		return row, err
	}
	var review storedReview
	if err := review.decode(raw); err != nil {
		return row, err
	}
	row.Consumed, row.Topics = review.Consumed, review.Topics
	return row, nil
}

func readBackupReport(rows *sql.Rows) (backupReport, error) {
	var row backupReport
	var report string
	var draft sql.NullString
	if err := rows.Scan(&row.Scope, &report, &row.ReportRevision, &draft, &row.DraftRevision); err != nil {
		return row, err
	}
	if err := json.Unmarshal([]byte(report), &row.Report); err != nil {
		return row, err
	}
	if draft.Valid {
		if err := json.Unmarshal([]byte(draft.String), &row.Draft); err != nil {
			return row, err
		}
	}
	return row, nil
}

func (backup workspaceBackup) valid() bool {
	for _, row := range backup.Topics {
		if !backupSessionID(row.SessionID) || row.Content == nil || slices.ContainsFunc(row.Content, invalidBackupTopic) {
			return false
		}
	}
	for _, row := range backup.Notes {
		if !backupSessionID(row.SessionID) || invalidBackupTopic(row.Content) {
			return false
		}
	}
	for _, id := range backup.ArchivedSessions {
		if !backupSessionID(id) {
			return false
		}
	}
	for _, row := range backup.Reviews {
		digest, err := hex.DecodeString(row.Revision)
		if !backupSessionID(row.SessionID) || err != nil || len(digest) != sha256.Size ||
			(row.Consumed < 1 && row.Consumed != -1) || slices.ContainsFunc(row.Topics, invalidBackupTopic) {
			return false
		}
	}
	for _, row := range backup.Reports {
		query, err := url.ParseQuery(row.Scope)
		from, to, windowErr := reviewWindow(query)
		_, knownKind := reportLayoutFor(query.Get("kind"), from, to)
		if err != nil || windowErr != nil || !knownKind || reportDocumentScope(query, from, to) != row.Scope ||
			!validBackupReport(row.Report) || row.ReportRevision <= 0 || row.ReportRevision == math.MaxInt64 ||
			row.DraftRevision < 0 || row.DraftRevision == math.MaxInt64 ||
			(row.Draft != nil && (row.DraftRevision == 0 || !validBackupReport(*row.Draft))) {
			return false
		}
	}
	return true
}

func backupSessionID(id string) bool {
	return strings.TrimSpace(id) != "" && !strings.ContainsRune(id, 0)
}

func invalidBackupTopic(topic distill.Topic) bool {
	return strings.TrimSpace(topic.Title) == "" || len(topic.Evidence) == 0 || slices.ContainsFunc(topic.Evidence, func(id string) bool {
		return !backupSessionID(id)
	})
}

func validBackupReport(report workReport) bool {
	return report.Sections != nil && (report.Mode == "local" || report.Mode == "model") &&
		!slices.ContainsFunc(report.Sections, invalidDraftSection)
}

func (a *app) importBackup(ctx context.Context, backup workspaceBackup) (backupImportResult, error) {
	var restored backupImportResult
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return restored, err
	}
	defer tx.Rollback()
	put := func(query string, args ...any) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			restored.Skipped++
		} else {
			restored.Added++
		}
		return nil
	}
	putContent := func(query string, session string, content any) error {
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		return put(query, session, string(raw))
	}
	for _, row := range backup.Topics {
		if err := putContent(`INSERT INTO topics(session, content) VALUES(?,?) ON CONFLICT(session) DO NOTHING`, row.SessionID, row.Content); err != nil {
			return restored, err
		}
	}
	for _, row := range backup.Notes {
		if err := putContent(`INSERT INTO notes(session, content) VALUES(?,?) ON CONFLICT(session, content) DO NOTHING`, row.SessionID, row.Content); err != nil {
			return restored, err
		}
	}
	for _, id := range backup.ArchivedSessions {
		if err := put(`INSERT INTO archived_sessions(session) VALUES(?) ON CONFLICT(session) DO NOTHING`, id); err != nil {
			return restored, err
		}
	}
	for _, row := range backup.Reviews {
		var value any = reviewCheckpoint{Consumed: row.Consumed, Topics: row.Topics}
		if row.Consumed == -1 {
			value = row.Topics
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return restored, err
		}
		if err := put(`INSERT INTO reviews(session, revision, content) VALUES(?,?,?) ON CONFLICT(session) DO NOTHING`, row.SessionID, row.Revision, string(raw)); err != nil {
			return restored, err
		}
	}
	for _, row := range backup.Reports {
		report, err := json.Marshal(row.Report)
		if err != nil {
			return restored, err
		}
		var draft any
		if row.Draft != nil {
			raw, err := json.Marshal(row.Draft)
			if err != nil {
				return restored, err
			}
			draft = string(raw)
		}
		if err := put(`INSERT INTO report_documents(scope, report, report_revision, draft, draft_revision)
			VALUES(?,?,?,?,?) ON CONFLICT(scope) DO NOTHING`, row.Scope, string(report), row.ReportRevision, draft, row.DraftRevision); err != nil {
			return restored, err
		}
	}
	return restored, tx.Commit()
}
