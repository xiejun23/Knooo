package studio

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

const searchPageSize = 20
const searchMatchSize = 6

type hit struct {
	SessionID    string        `json:"sessionId"`
	Agent        string        `json:"agent"`
	AgentVariant string        `json:"agentVariant,omitempty"`
	Project      string        `json:"project"`
	Topic        distill.Topic `json:"topic"`
	Matches      []match       `json:"matches"`
	MoreMatches  []string      `json:"moreMatches,omitempty"`
	rank         int
}

type match struct {
	SessionID       string `json:"sessionId"`
	MessageID       string `json:"messageId"`
	Snippet         string `json:"snippet"`
	EvidenceVersion string `json:"evidenceVersion,omitempty"`
}

type searchPage struct {
	Hits     []hit `json:"hits"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int   `json:"total"`
}

type searchMatchPage struct {
	Matches []match `json:"matches"`
	HasMore bool    `json:"hasMore"`
	exact   bool
}

func textSnippet(text, q string) string {
	folded := strings.ToLower(text)
	n := strings.Index(folded, q)
	if n < 0 {
		return ""
	}
	return snippetAt(text, folded, n)
}

func snippetAt(text, folded string, n int) string {
	start := max(0, utf8.RuneCountInString(folded[:n])-24)
	for range start {
		_, size := utf8.DecodeRuneInString(text)
		text = text[size:]
	}
	runes := 0
	for i := range text {
		if runes == 128 {
			text = text[:i]
			break
		}
		runes++
	}
	return strings.Join(strings.Fields(text), " ")
}

func identifierRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-'
}

func identifierQuery(query string) bool {
	for _, r := range query {
		if r > unicode.MaxASCII || !identifierRune(r) {
			return false
		}
	}
	return true
}

func exactIdentifier(text, query string, first int) bool {
	for at := first; at >= 0; {
		end := at + len(query)
		before, _ := utf8.DecodeLastRuneInString(text[:at])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if !identifierRune(before) && !identifierRune(after) {
			return true
		}
		next := strings.Index(text[end:], query)
		if next < 0 {
			break
		}
		at = end + next
	}
	return false
}

func searchSessionMatches(ctx context.Context, s sessions.Session, query string, offset int, rankIdentifier bool) searchMatchPage {
	result := searchMatchPage{Matches: []match{}}
	for _, m := range s.Messages {
		if ctx.Err() != nil {
			break
		}
		folded := strings.ToLower(m.Text)
		at := strings.Index(folded, query)
		if at < 0 {
			continue
		}
		if rankIdentifier && !result.exact {
			result.exact = exactIdentifier(folded, query, at)
		}
		if offset > 0 {
			offset--
			continue
		}
		if len(result.Matches) == searchMatchSize {
			result.HasMore = true
			if !rankIdentifier || result.exact {
				break
			}
			continue
		}
		result.Matches = append(result.Matches, match{SessionID: s.ID, MessageID: m.ID, Snippet: snippetAt(m.Text, folded, at), EvidenceVersion: messageEvidenceVersion(m)})
	}
	return result
}

func searchQuery(args url.Values) string {
	return strings.ToLower(strings.TrimSpace(args.Get("q")))
}

func searchInScope(s sessions.Session, args url.Values) bool {
	return inProject(s, args.Get("project"), args.Get("location")) && s.MatchesAgent(args["agent"]...)
}

func searchRank(s sessions.Session, q string) int {
	if strings.EqualFold(s.Title, q) || strings.EqualFold(s.ID, q) {
		return 3
	}
	pathQuery := strings.ReplaceAll(q, `\`, "/")
	for _, path := range []string{s.ProjectPath, s.ProjectDisplayPath, s.Cwd, s.Path, s.SourceRoot} {
		if strings.EqualFold(strings.ReplaceAll(path, `\`, "/"), pathQuery) {
			return 3
		}
	}
	if matchesSessionMetadata(s, q) {
		return 1
	}
	return 0
}

func (a *app) search(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	q := searchQuery(args)
	result := []hit{}
	page := 0
	if args.Has("page") {
		var err error
		page, err = strconv.Atoi(args.Get("page"))
		if err != nil || page < 1 {
			http.Error(w, "页码无效", http.StatusBadRequest)
			return
		}
	}
	if q == "" {
		if page > 0 {
			jsonReply(w, searchPage{Hits: result, Page: 1, PageSize: searchPageSize})
		} else {
			jsonReply(w, result)
		}
		return
	}
	a.ensureSessionIndex()
	ctx := r.Context()
	archived, err := a.archivedSessions(ctx)
	if err != nil {
		http.Error(w, "无法读取会话归档", 500)
		return
	}
	saved, err := a.allTopics(ctx)
	if err != nil {
		http.Error(w, "无法读取提炼结果", 500)
		return
	}
	groups := map[[7]string]int{}
	rankIdentifier := identifierQuery(q)
	for _, s := range a.sessionsSnapshot() {
		if ctx.Err() != nil {
			return
		}
		if archived[s.ID] || !searchInScope(s, args) {
			continue
		}
		metadata := searchRank(s, q)
		matchPage := searchSessionMatches(ctx, s, q, 0, rankIdentifier && metadata < 3)
		matches := matchPage.Matches
		if len(matches) == 0 {
			if metadata == 0 {
				continue
			}
			matches = append(matches, match{SessionID: s.ID})
		}
		t := distill.Topic{Title: s.Title, Problem: matches[0].Snippet, Status: "observed"}
		best := 0
		for _, candidate := range checkTopicEvidence(saved[s.ID], s.Messages) {
			if candidate.EvidenceStale {
				continue
			}
			score := 0
			for _, m := range matches {
				if m.MessageID != "" && slices.Contains(candidate.Evidence, m.MessageID) {
					score++
				}
			}
			if score > best {
				t, best = candidate, score
			}
		}
		item := hit{SessionID: s.ID, Agent: s.Agent, AgentVariant: s.AgentVariant, Project: s.Project, Topic: t, Matches: matches, rank: metadata}
		if matchPage.exact {
			item.rank = 2
		}
		if matchPage.HasMore {
			item.MoreMatches = []string{s.ID}
		}
		// shortcut: compare complete issue text exactly until we have a reliable semantic matcher.
		key := [7]string{projectPath(s), s.Agent, normalize(t.Title), normalize(t.Problem), normalize(t.Outcome), t.Status, sessionLocation(s)}
		if best > 0 && key[2] != "" && key[3] != "" && key[0] != "" {
			if index, ok := groups[key]; ok {
				existing := &result[index]
				existing.rank = max(existing.rank, item.rank)
				existing.Matches = append(existing.Matches, item.Matches...)
				existing.MoreMatches = append(existing.MoreMatches, item.MoreMatches...)
				continue
			}
			groups[key] = len(result)
		}
		result = append(result, item)
	}
	if ctx.Err() != nil {
		return
	}
	slices.SortStableFunc(result, func(a, b hit) int { return cmp.Compare(b.rank, a.rank) })
	if page == 0 {
		jsonReply(w, result[:min(100, len(result))])
		return
	}
	total := len(result)
	page = min(page, max(1, (total+searchPageSize-1)/searchPageSize))
	start := (page - 1) * searchPageSize
	jsonReply(w, searchPage{Hits: result[start:min(start+searchPageSize, total)], Page: page, PageSize: searchPageSize, Total: total})
}

func (a *app) searchMatches(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	q := searchQuery(args)
	offset, err := strconv.Atoi(args.Get("offset"))
	if err != nil || offset < 0 || q == "" {
		http.Error(w, "检索位置无效", 400)
		return
	}
	s, ok := a.session(args.Get("session"))
	if !ok || !searchInScope(s, args) {
		http.Error(w, "会话暂不可用", 404)
		return
	}
	archived, err := a.archivedSessions(r.Context())
	if err != nil {
		http.Error(w, "无法读取会话归档", 500)
		return
	}
	if archived[s.ID] {
		http.Error(w, "会话暂不可用", 404)
		return
	}
	result := searchSessionMatches(r.Context(), s, q, offset, false)
	if r.Context().Err() == nil {
		jsonReply(w, result)
	}
}

func normalize(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
