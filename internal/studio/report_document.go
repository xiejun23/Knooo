package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

func reportDocumentScope(query url.Values, from, to time.Time) string {
	agents := slices.DeleteFunc(slices.Clone(query["agent"]), func(agent string) bool { return agent == "" })
	slices.Sort(agents)
	scope := url.Values{
		"kind": {query.Get("kind")}, "from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)},
		"project": {query.Get("project")}, "location": {query.Get("location")}, "agent": slices.Compact(agents),
	}
	return scope.Encode()
}

func requestedReportScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	query := r.URL.Query()
	from, to, err := reviewWindow(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	if _, ok := reportLayoutFor(query.Get("kind"), from, to); !ok {
		http.Error(w, "无效的报告类型", http.StatusBadRequest)
		return "", false
	}
	return reportDocumentScope(query, from, to), true
}

func (a *app) storeGeneratedReport(ctx context.Context, scope string, report workReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO report_documents(scope,report) VALUES(?,?)
		ON CONFLICT(scope) DO UPDATE SET report=excluded.report, report_revision=report_revision+1`, scope, string(body))
	return err
}

type reportDocument struct {
	Report         json.RawMessage `json:"report"`
	ReportRevision int64           `json:"reportRevision"`
	Draft          json.RawMessage `json:"draft"`
	DraftRevision  int64           `json:"draftRevision"`
}

func (a *app) savedReport(w http.ResponseWriter, r *http.Request) {
	scope, ok := requestedReportScope(w, r)
	if !ok {
		return
	}
	var result reportDocument
	var report string
	var draft sql.NullString
	err := a.db.QueryRowContext(r.Context(), `SELECT report,report_revision,draft,draft_revision FROM report_documents WHERE scope=?`, scope).
		Scan(&report, &result.ReportRevision, &draft, &result.DraftRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil || !json.Valid([]byte(report)) || (draft.Valid && !json.Valid([]byte(draft.String))):
		http.Error(w, "无法读取已保存的报告", http.StatusInternalServerError)
		return
	default:
		result.Report = json.RawMessage(report)
		if draft.Valid {
			result.Draft = json.RawMessage(draft.String)
		}
	}
	jsonReply(w, result)
}

func (a *app) saveReportDraft(w http.ResponseWriter, r *http.Request) {
	scope, ok := requestedReportScope(w, r)
	if !ok {
		return
	}
	var input struct {
		Draft          json.RawMessage `json:"draft"`
		DraftRevision  *int64          `json:"draftRevision"`
		ReportRevision int64           `json:"reportRevision"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil || d.Decode(new(any)) != io.EOF || input.DraftRevision == nil ||
		*input.DraftRevision < 0 || len(input.Draft) == 0 {
		http.Error(w, "报告编辑内容无效或过长", http.StatusBadRequest)
		return
	}
	var draft any
	if string(input.Draft) != "null" {
		var edited workReport
		if err := json.Unmarshal(input.Draft, &edited); err != nil || edited.Sections == nil ||
			(edited.Mode != "local" && edited.Mode != "model") || slices.ContainsFunc(edited.Sections, invalidDraftSection) {
			http.Error(w, "报告编辑内容无效", http.StatusBadRequest)
			return
		}
		draft = string(input.Draft)
	} else if input.ReportRevision <= 0 {
		http.Error(w, "生成稿版本无效", http.StatusBadRequest)
		return
	}
	var result struct {
		DraftRevision int64 `json:"draftRevision"`
	}
	err := a.db.QueryRowContext(r.Context(), `UPDATE report_documents SET draft=?, draft_revision=draft_revision+1
		WHERE scope=? AND draft_revision=? AND (? IS NOT NULL OR report_revision=?) RETURNING draft_revision`,
		draft, scope, *input.DraftRevision, draft, input.ReportRevision).Scan(&result.DraftRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "已保存的报告发生变化。可导出当前内容，或放弃本次编辑查看已保存的版本。", http.StatusConflict)
	case err != nil:
		http.Error(w, "无法保存报告编辑，请重试", http.StatusInternalServerError)
	default:
		jsonReply(w, result)
	}
}

func invalidDraftSection(section reportSection) bool {
	return section.Items == nil || slices.ContainsFunc(section.Items, func(item reportItem) bool { return item.Sources == nil })
}
