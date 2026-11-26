package distill

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBudgetLimitsTranscriptAndEvidence(t *testing.T) {
	config := Config{Provider: "gateway", Model: "vendor/example", InputBudget: 4096, OutputBudget: 1024}
	model := Model{ContextWindow: 2700, MaxOutputTokens: 512}
	messages := []Input{
		{ID: "old", Role: "user", Text: strings.Repeat("已略过", 300)},
		{ID: "new", Role: "assistant", Text: "START" + strings.Repeat("你好 < & \"\n", 1000) + "END"},
	}
	plan, err := Prepare(config, model, messages)
	if err != nil {
		t.Fatal(err)
	}
	if plan.InputBudget != 2188 || plan.OutputBudget != 512 || plan.EstimatedInput > plan.InputBudget ||
		plan.EstimatedInput+plan.OutputBudget > model.ContextWindow || plan.Included != 1 || !plan.Shortened {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	var sent []Input
	if err := json.Unmarshal([]byte(plan.transcript), &sent); err != nil || len(sent) != 1 || sent[0].ID != "new" ||
		!strings.HasPrefix(sent[0].Text, "START") || !strings.HasSuffix(sent[0].Text, "END") || !strings.Contains(sent[0].Text, "…") {
		t.Fatalf("invalid clip: %v, %v", sent, err)
	}
	if _, err := parseTopics(strings.Replace(topicJSON, "m1", "old", 1), plan.refs); err == nil {
		t.Fatal("omitted evidence must be rejected")
	}
	messages[1].Text += " updated"
	updated, err := Prepare(config, model, messages)
	if err != nil || updated.Confirmation == plan.Confirmation {
		t.Fatal("changed request must require confirmation again")
	}
}

func TestAnthropicPlanMatchesRequestBudget(t *testing.T) {
	config := Config{Provider: "anthropic", Model: "example", APIKey: fakeKey, InputBudget: 8192, OutputBudget: 1024, ReasoningEffort: "high"}
	model := Model{ContextWindow: 10000, MaxOutputTokens: 4500}
	messages := []Input{{ID: "m1", Role: "user", Text: "Review the requested change"}}
	plan, err := Prepare(config, model, messages)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ThinkingBudget != 4096 || plan.OutputBudget != 404 || plan.TotalOutputBudget != 4500 ||
		plan.InputBudget+plan.TotalOutputBudget != model.ContextWindow {
		t.Fatalf("unexpected combined budget: %+v", plan)
	}
	_, err = plan.generate(context.Background(), config, testTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		var payload struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || payload.MaxTokens != plan.TotalOutputBudget {
			t.Fatalf("provider request must use confirmed limit: %+v (%v)", payload, err)
		}
		return mockResponse(http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":`+jsonString(topicJSON)+`}]}`), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	config.ReasoningEffort = "low"
	updated, err := Prepare(config, model, messages)
	if err != nil || updated.Confirmation == plan.Confirmation {
		t.Fatalf("reasoning budget change should require confirmation: %+v (%v)", updated, err)
	}
}
