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
		facts[i] = ReportFact{Title: fmt.Sprint("主题", i), Outcome: strings.Repeat("验证结果", 18), Status: "verified", Type: ReportTesting}
	}
	facts[1].Outcome = strings.Repeat("超限文本", 400)
	for _, ref := range []int{1, 2, 9} {
		t.Run(fmt.Sprint(ref), func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Max      int `json:"max_tokens"`
					Messages []struct{ Content string }
				}
				var packed []struct{ Source int }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Max != 256 || len(body.Messages) != 2 ||
					len(body.Messages[0].Content)+len(body.Messages[1].Content)+128 > 2048 ||
					json.Unmarshal([]byte(body.Messages[1].Content), &packed) != nil || len(packed) < 2 || len(packed) > 8 ||
					packed[0].Source != 1 || packed[1].Source != 3 {
					t.Errorf("unexpected budget: %+v, facts %+v (%v)", body, packed, err)
				}
				content := fmt.Sprintf(`{"items":[{"type":"testing","title":"验证完成","text":"回归检查通过。","sources":[%d]}]}`, ref)
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
			}))
			defer gateway.Close()
			config := Config{Provider: "gateway", Model: "mock", BaseURL: gateway.URL, InputBudget: 2048, OutputBudget: 256}
			result, err := SynthesizeReport(context.Background(), config, Model{}, "具体成果", 6, facts)
			if ref == 1 && (err != nil || !result.Shortened || len(result.Items) != 1) || ref != 1 && err == nil {
				t.Fatalf("unexpected result for reference %d: %+v (%v)", ref, result, err)
			}
		})
	}
}

func TestReportSynthesisValidatesAssociationAndKeepsAcceptedRows(t *testing.T) {
	facts := map[int]ReportFact{
		1: {Status: "verified", Project: "P1", Work: 1},
		2: {Status: "open", Project: "P1", Work: 1},
		3: {Status: "resolved", Project: "P1", Work: 2},
		4: {Status: "verified", Project: "P2", Work: 1},
		5: {Project: "P1"},
		6: {Status: "open"},
		7: {Status: "verified"},
	}
	for _, sources := range []string{"[1,3]", "[1,4]", "[1,5]", "[6,7]", "[1,999]"} {
		content := `{"items":[{"type":"testing","title":"同一标题","text":"合并记录。","sources":` + sources + `},` +
			`{"type":"testing","title":"同一标题","text":"同一事项仍待补充。","sources":[1,2,1]},` +
			`{"type":"testing","title":"同一标题","text":"第二项完成。","sources":[3]},` +
			`{"type":"question","title":"提问","text":"等待回答。","sources":[5]}]}`
		result, err := parseReportSynthesis(content, facts, 2)
		if err != nil || !result.Shortened || len(result.Items) != 2 || result.Items[0].Text != "同一事项仍待补充。" ||
			len(result.Items[0].Sources) != 2 || result.Items[1].Sources[0] != 3 {
			t.Fatalf("unexpected result for sources %s: %+v (%v)", sources, result, err)
		}
	}
	wrapped := "以下是汇总报告：\n```JSON\n" + `{"items":[{"type":"testing","title":"回归检查","text":"已完成。","sources":[1]}]}` + "\n```\n供参考。"
	result, err := parseReportSynthesis(wrapped, facts, 2)
	if err != nil || len(result.Items) != 1 || result.Items[0].Title != "回归检查" {
		t.Fatalf("wrapped report parse failed: %+v (%v)", result, err)
	}
}
