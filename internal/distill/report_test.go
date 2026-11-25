package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportSynthesisBoundsAndValidatesPackedFacts(t *testing.T) {
	facts := make([]ReportFact, 9)
	for i := range facts {
		facts[i] = ReportFact{Title: fmt.Sprint("主题", i), Outcome: strings.Repeat("验证结果", 100), Status: "verified"}
	}
	for _, ref := range []int{1, 9} {
		t.Run(fmt.Sprint(ref), func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Max      int `json:"max_tokens"`
					Messages []struct{ Content string }
				}
				var packed []struct{ Source int }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Max != 256 || len(body.Messages) != 2 ||
					len(body.Messages[0].Content)+len(body.Messages[1].Content)+128 > 2048 ||
					json.Unmarshal([]byte(body.Messages[1].Content), &packed) != nil || len(packed) < 2 || len(packed) > 8 {
					t.Errorf("unexpected budget: %+v, facts %+v (%v)", body, packed, err)
				}
				content := fmt.Sprintf(`{"sections":[{"category":"outcome","items":[{"title":"验证完成","text":"回归检查通过。","sources":[%d]}]}]}`, ref)
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
			}))
			defer gateway.Close()
			config := Config{Provider: "gateway", Model: "mock", BaseURL: gateway.URL, InputBudget: 2048, OutputBudget: 256}
			result, err := SynthesizeReport(context.Background(), config, Model{}, "具体成果", 6, facts)
			if ref == 1 && (err != nil || !result.Shortened || len(result.Sections) != 1) || ref == 9 && err == nil {
				t.Fatalf("unexpected result for reference %d: %+v (%v)", ref, result, err)
			}
		})
	}
}

func TestReportSynthesisRejectsTooManyItems(t *testing.T) {
	item := `{"title":"验证完成","text":"回归检查通过。","sources":[1]}`
	content := `{"sections":[{"category":"outcome","items":[` + strings.Repeat(item+",", 6) + item + `]}]}`
	if _, err := parseReportSynthesis(content, map[int]ReportCategory{1: ReportOutcome}, 6); err == nil {
		t.Fatal("accepted more than six daily items")
	}
}
