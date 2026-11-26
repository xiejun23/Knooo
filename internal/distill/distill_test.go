package distill

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	fakeKey     = "test-only-key"
	privateText = "private transcript marker"
	topicJSON   = `{"topics":[{"title":"Session topic","problem":"Example","outcome":"","status":"observed","lesson":"","exercise":"","evidence":["m1"]}]}`
)

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func generate(ctx context.Context, config Config, messages []Input, transport http.RoundTripper) ([]Topic, error) {
	plan, err := Prepare(config, Model{}, messages)
	if err != nil {
		return nil, err
	}
	return plan.generate(ctx, config, transport)
}

func TestGenerateProviders(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		endpoint string
		headers  map[string]string
		wrap     func(string) string
	}{
		{
			provider: "openai",
			model:    "test-model",
			endpoint: "https://api.openai.com/v1/chat/completions",
			headers:  map[string]string{"Authorization": "Bearer " + fakeKey},
			wrap: func(text string) string {
				return `{"choices":[{"finish_reason":"stop","message":{"content":` + jsonString(text) + `}}]}`
			},
		},
		{
			provider: "anthropic",
			model:    "test-model",
			endpoint: "https://api.anthropic.com/v1/messages",
			headers:  map[string]string{"x-api-key": fakeKey, "anthropic-version": "2023-06-01"},
			wrap: func(text string) string {
				return `{"stop_reason":"end_turn","content":[{"type":"thinking","thinking":"skip"},` +
					`{"type":"text","text":` + jsonString(text[:20]) + `},{"type":"text","text":` + jsonString(text[20:]) + `}]}`
			},
		},
		{
			provider: "gemini",
			model:    "models/test-model",
			endpoint: "https://generativelanguage.googleapis.com/v1beta/models/test-model:generateContent",
			headers:  map[string]string{"x-goog-api-key": fakeKey},
			wrap: func(text string) string {
				return `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"thought":true,"text":"skip"},` +
					`{"text":` + jsonString("```json\n"+text+"\n```") + `}]}}]}`
			},
		},
	}
	input := []Input{{ID: "m1", Role: "user", Text: privateText}, {ID: "m2", Role: "tool", Text: "tool output"}}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			transport := testTransport(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if req.Method != http.MethodPost || req.URL.String() != tt.endpoint {
					t.Fatalf("unexpected request %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("missing JSON content type")
				}
				for _, header := range []string{"Authorization", "x-api-key", "anthropic-version", "x-goog-api-key"} {
					if req.Header.Get(header) != tt.headers[header] {
						t.Fatalf("unexpected %s header", header)
					}
				}

				var payload struct {
					Model    string `json:"model"`
					System   string `json:"system"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					MaxCompletionTokens int `json:"max_completion_tokens"`
					MaxTokens           int `json:"max_tokens"`
					Instruction         struct {
						Parts []struct{ Text string } `json:"parts"`
					} `json:"systemInstruction"`
					Contents []struct {
						Role  string                  `json:"role"`
						Parts []struct{ Text string } `json:"parts"`
					} `json:"contents"`
					GenerationConfig struct {
						MaxOutputTokens int `json:"maxOutputTokens"`
					} `json:"generationConfig"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				var prompt, transcript string
				switch tt.provider {
				case "openai":
					if payload.Model != tt.model || payload.MaxCompletionTokens <= 0 ||
						len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[1].Role != "user" {
						t.Fatal("invalid OpenAI request")
					}
					prompt, transcript = payload.Messages[0].Content, payload.Messages[1].Content
				case "anthropic":
					if payload.Model != tt.model || payload.MaxTokens <= 0 ||
						len(payload.Messages) != 1 || payload.Messages[0].Role != "user" {
						t.Fatal("invalid Anthropic request")
					}
					prompt, transcript = payload.System, payload.Messages[0].Content
				case "gemini":
					if payload.GenerationConfig.MaxOutputTokens <= 0 ||
						len(payload.Instruction.Parts) != 1 || len(payload.Contents) != 1 ||
						payload.Contents[0].Role != "user" || len(payload.Contents[0].Parts) != 1 {
						t.Fatal("invalid Gemini request")
					}
					prompt, transcript = payload.Instruction.Parts[0].Text, payload.Contents[0].Parts[0].Text
				}
				var sent []Input
				if prompt != instructions || json.Unmarshal([]byte(transcript), &sent) != nil || !reflect.DeepEqual(sent, input) {
					t.Fatal("request did not preserve the instruction and transcript")
				}
				return mockResponse(http.StatusOK, tt.wrap(topicJSON)), nil
			})

			topics, err := generate(context.Background(), Config{Provider: tt.provider, Model: tt.model, APIKey: fakeKey}, input, transport)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(topics)
			want := strings.TrimSuffix(strings.TrimPrefix(topicJSON, `{"topics":`), "}")
			if err != nil || string(got) != want {
				t.Fatalf("topics = %s, want %s (error: %v)", got, want, err)
			}
		})
	}
}

func TestGenerateResponseErrors(t *testing.T) {
	tests := []struct {
		name     string
		response func() (*http.Response, error)
		want     string
	}{
		{"HTTP error", func() (*http.Response, error) {
			return mockResponse(http.StatusUnauthorized, fakeKey+privateText), nil
		}, "HTTP 401"},
		{"redirect", func() (*http.Response, error) {
			response := mockResponse(http.StatusTemporaryRedirect, "")
			response.Header.Set("Location", "https://example.invalid/")
			return response, nil
		}, "HTTP 307"},
		{"transport error", func() (*http.Response, error) {
			return nil, errors.New(fakeKey + privateText)
		}, "model request failed"},
		{"oversized declared response", func() (*http.Response, error) {
			response := mockResponse(http.StatusOK, "")
			response.ContentLength = maxResponseBytes + 1
			return response, nil
		}, "exceeds size limit"},
		{"oversized streamed response", func() (*http.Response, error) {
			response := mockResponse(http.StatusOK, strings.Repeat(" ", maxResponseBytes+1))
			response.ContentLength = -1
			return response, nil
		}, "exceeds size limit"},
		{"malformed provider response", func() (*http.Response, error) {
			return mockResponse(http.StatusOK, fakeKey+privateText), nil
		}, "invalid provider response"},
		{"truncated completion", func() (*http.Response, error) {
			body := `{"choices":[{"finish_reason":"length","message":{"content":` + jsonString(topicJSON) + `}}]}`
			return mockResponse(http.StatusOK, body), nil
		}, "输出上限"},
		{"bad evidence", func() (*http.Response, error) {
			body := `{"choices":[{"message":{"content":` + jsonString(strings.Replace(topicJSON, "m1", privateText, 1)) + `}}]}`
			return mockResponse(http.StatusOK, body), nil
		}, "invalid topic evidence reference"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := testTransport(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if req.URL.Host != "api.openai.com" {
					t.Fatal("request followed a redirect")
				}
				return tt.response()
			})
			config := Config{Provider: "openai", Model: "test-model", APIKey: fakeKey}
			topics, err := generate(context.Background(), config, []Input{{ID: "m1", Text: privateText}}, transport)
			if topics != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("topics = %v, error = %v, want error containing %q", topics, err, tt.want)
			}
			if strings.Contains(err.Error(), fakeKey) || strings.Contains(err.Error(), privateText) {
				t.Fatal("error leaked request data")
			}
		})
	}
}

func TestGenerateRejectsInvalidRequests(t *testing.T) {
	validInput := []Input{{ID: "m1", Text: privateText}}
	tests := []struct {
		config   Config
		messages []Input
	}{
		{Config{Provider: "other", Model: "test-model", APIKey: fakeKey}, validInput},
		{Config{Provider: "openai", Model: "test-model"}, validInput},
		{Config{Provider: "openai", Model: "test-model", APIKey: "bad\r\nkey"}, validInput},
		{Config{Provider: "gemini", Model: "../other", APIKey: fakeKey}, validInput},
		{Config{Provider: "openai", Model: "test-model", APIKey: fakeKey}, []Input{{ID: "m1", Text: privateText}, {ID: "m1", Text: "duplicate"}}},
		{Config{Provider: "openai", Model: "test-model", APIKey: fakeKey}, []Input{{ID: " ", Text: privateText}}},
	}
	transport := testTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached the transport")
		return nil, nil
	})
	for _, tt := range tests {
		if _, err := generate(context.Background(), tt.config, tt.messages, transport); err == nil {
			t.Error("invalid request succeeded")
		}
	}
}

func TestGeneratePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := testTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		return nil, req.Context().Err()
	})
	config := Config{Provider: "openai", Model: "test-model", APIKey: fakeKey}
	_, err := generate(ctx, config, []Input{{ID: "m1", Text: privateText}}, transport)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}

func TestParseTopics(t *testing.T) {
	refs := map[string]int{"m1": 1}
	for _, text := range []string{
		`{"topics":null}`,
		`{"topics":[null]}`,
		`{"topics":[]} {"topics":[]}`,
		strings.Replace(topicJSON, `"observed"`, `"unknown"`, 1),
		strings.Replace(topicJSON, `["m1"]`, `[]`, 1),
		strings.Replace(topicJSON, `["m1"]`, `[" m1 "]`, 1),
		"```json\n" + topicJSON,
	} {
		if _, err := parseTopics(text, refs); err == nil {
			t.Errorf("parseTopics(%q) succeeded", text)
		}
	}
	if topics, err := parseTopics(`{"topics":[]}`, refs); err != nil || topics == nil || len(topics) != 0 {
		t.Fatalf("empty topics = %v, error = %v", topics, err)
	}
	mixed := strings.Replace(topicJSON, `["m1"]`, `["m1","ghost-99","m1"]`, 1)
	if topics, err := parseTopics(mixed, refs); err != nil || len(topics) != 1 || !reflect.DeepEqual(topics[0].Evidence, []string{"m1"}) {
		t.Fatalf("mixed evidence = %#v, err = %v", topics, err)
	}
	if _, err := parseTopics(strings.Replace(topicJSON, `["m1"]`, `["ghost-99"]`, 1), refs); err == nil {
		t.Fatal("all-invalid evidence should still fail")
	}
}

func TestLocal(t *testing.T) {
	userText := strings.Repeat("实", 400)
	messages := []Input{
		{ID: "duplicate", Role: "user", Text: "ambiguous"},
		{ID: "duplicate", Role: "user", Text: "also ambiguous"},
		{Role: "user", Text: "missing anchor"},
		{ID: "assistant", Role: "assistant", Text: "An answer"},
		{ID: "user", Role: "User", Text: "  " + userText + "  "},
	}
	want := []Topic{{Title: strings.Repeat("实", 72), Problem: strings.Repeat("实", 320), Status: "observed", Evidence: []string{"user"}}}
	for range 2 {
		if got := Local("", messages); !reflect.DeepEqual(got, want) {
			t.Fatalf("Local = %#v, want %#v", got, want)
		}
	}
	want = []Topic{{Title: "Session", Problem: "An answer", Status: "observed", Evidence: []string{"assistant"}}}
	if got := Local(" Session ", messages[:4]); !reflect.DeepEqual(got, want) {
		t.Fatalf("Local fallback = %#v, want %#v", got, want)
	}
	if got := Local("Session", messages[:3]); len(got) != 0 {
		t.Fatalf("Local without usable anchors = %#v", got)
	}
}

func jsonString(text string) string {
	data, _ := json.Marshal(text)
	return string(data)
}

func mockResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        make(http.Header),
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
	}
}

func TestListModelsPagination(t *testing.T) {
	for _, tc := range []struct {
		provider, base, credential, first, query, second string
	}{
		{
			"anthropic", "https://api.anthropic.com/v1/models", "x-api-key",
			`{"data":[{"id":"model-z"}],"has_more":true,"last_id":"id + token"}`,
			"after_id=id+%2B+token", `{"data":[{"id":"model-a"},{"id":"model-z"}],"has_more":false}`,
		},
		{
			"gemini", "https://generativelanguage.googleapis.com/v1beta/models", "x-goog-api-key",
			`{"models":[{"name":"models/model-z","supportedGenerationMethods":["generateContent"]},{"name":"models/embedding","supportedGenerationMethods":["embedContent"]}],"nextPageToken":"id + token"}`,
			"pageToken=id+%2B+token", `{"models":[{"name":"models/model-a","supportedGenerationMethods":["generateContent"]}]}`,
		},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			endpoints := []string{tc.base, tc.base + "?" + tc.query}
			pages := []string{tc.first, tc.second}
			index := 0
			models, err := listModels(context.Background(), Config{Provider: tc.provider, APIKey: fakeKey}, testTransport(func(req *http.Request) (*http.Response, error) {
				if index >= len(endpoints) || req.URL.String() != endpoints[index] || req.Header.Get(tc.credential) != fakeKey || req.Method != http.MethodGet || req.Body != nil {
					t.Fatalf("unexpected models request: %s %s", req.Method, req.URL)
				}
				response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(pages[index]))}
				index++
				return response, nil
			}))
			if err != nil || index != 2 || !reflect.DeepEqual(models, []Model{{ID: "model-a"}, {ID: "model-z"}}) {
				t.Fatalf("models %v after %d requests: %v", models, index, err)
			}
		})
	}
}

func TestReviewBoundaryContextAndConciseValidation(t *testing.T) {
	history := []Input{
		{ID: "u0", Role: "user", Text: "最早期的问题，不应进入近两轮边界上下文。"},
		{ID: "a0", Role: "assistant", Text: "早期回复。"},
		{ID: "u1", Role: "user", Text: "帮我排查订单接口超时问题。"},
		{ID: "a1", Role: "assistant", Text: "已定位到缺少组合索引。"},
		{ID: "u2", Role: "user", Text: "好的谢谢。"},
		{ID: "u3", Role: "user", Text: "给订单表加上组合索引后再压测一下。"},
		{ID: "a3", Role: "assistant", Text: "已加索引，平均耗时降到 20ms。"},
		{ID: "u4", Role: "user", Text: "这个索引顺便也加到备份库里。"},
	}
	overlap := reviewBoundaryContext(history, 7, "", 900)
	if !strings.Contains(overlap, "帮我排查订单接口超时问题") || !strings.Contains(overlap, "给订单表加上组合索引后再压测一下") || strings.Contains(overlap, "最早期的问题") {
		t.Fatalf("unexpected 1-2 turn boundary overlap: %q", overlap)
	}

	concise := []Input{
		{ID: "m1", Role: "user", Text: "排查企业认证图片 URL 消失"},
		{ID: "m2", Role: "assistant", Text: "发现端口拼接异常，已修复。"},
	}
	if err := validateReviewTopic(Topic{
		Title: "排查企业认证图片 URL 消失", Problem: "排查企业认证图片 URL 消失",
		Approach: "定位并修正端口拼接逻辑", Outcome: "图片 URL 恢复正常展示",
		Status: "resolved", Evidence: []string{"m1", "m2"},
	}, concise); err != nil {
		t.Fatalf("concise prompt reuse should be allowed: %v", err)
	}

	longRaw := strings.Repeat("这是一段没有经过提炼直接整段照抄的原始对话内容用来验证大段照抄仍然会被拦截", 3)
	if err := validateReviewTopic(Topic{
		Title: "长段照抄测试", Problem: longRaw,
		Status: "open", Evidence: []string{"m1"},
	}, []Input{{ID: "m1", Role: "user", Text: longRaw}}); err == nil {
		t.Fatal("expected long verbatim dump (>= 80 runes) to still be rejected")
	}
}

func TestReviewKeepsNewMessagesWhenResumingWithSmallBudget(t *testing.T) {
	messages := []Input{
		{ID: "u1", Role: "user", Text: "核对上一次缓存修改。"},
		{ID: "a1", Role: "assistant", Text: strings.Repeat("已完成缓存回归。", 15)},
		{ID: "u2", Role: "user", Text: strings.Repeat("请继续检查索引。", 20)},
		{ID: "a2", Role: "assistant", Text: strings.Repeat("已核对索引覆盖范围。", 20)},
		{ID: "u3", Role: "user", Text: "复核排序字段。"},
	}
	want := [][]string{{"u2", "a2"}, {"u3"}}
	const budget = 4096
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		var batch []Input
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 ||
			json.Unmarshal([]byte(body.Messages[1].Content), &batch) != nil ||
			len(body.Messages[0].Content)+len(body.Messages[1].Content)+128 > budget {
			t.Error("expected a batch that fits the configured budget")
			http.Error(w, "invalid batch", http.StatusBadRequest)
			return
		}
		var ids []string
		for _, message := range batch {
			ids = append(ids, message.ID)
		}
		if len(want) == 0 || !slices.Equal(ids, want[0]) {
			t.Errorf("unexpected review batch: %v", ids)
			http.Error(w, "unexpected batch", http.StatusBadRequest)
			return
		}
		want = want[1:]
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"topics\":[]}"}}]}`)
	}))
	defer gateway.Close()
	config := Config{Provider: "gateway", Model: "review-test", BaseURL: gateway.URL + "/v1", InputBudget: budget, OutputBudget: 256}
	topics := []Topic{{Title: "缓存检查", Problem: "核对修改", Evidence: []string{"u1", "a1"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var checkpoints []int
	err := Review(ctx, config, Model{}, messages, 3, topics, func(consumed int, got []Topic) error {
		checkpoints = append(checkpoints, consumed)
		if len(got) != 1 || got[0].Title != topics[0].Title {
			t.Fatalf("expected saved topics to carry forward: %+v", got)
		}
		return nil
	})
	if err != nil || !slices.Equal(checkpoints, []int{4, 5}) {
		t.Fatalf("checkpoints=%v, err=%v", checkpoints, err)
	}
}

func TestReviewHonorsOutputBudgetAndSplitsTruncatedBatch(t *testing.T) {
	messages := []Input{
		{ID: "u1", Role: "user", Text: "请检查缓存。"},
		{ID: "a1", Role: "assistant", Text: "缓存检查完成。"},
		{ID: "u2", Role: "user", Text: "请检查排序。"},
		{ID: "a2", Role: "assistant", Text: "排序检查完成。"},
	}
	for _, test := range []struct {
		name              string
		modelLimit        int
		wantBudget        int
		truncateFirstTurn bool
	}{
		{name: "connection limit", wantBudget: MaxOutputBudget},
		{name: "model limit", modelLimit: 16000, wantBudget: 16000},
		{name: "still truncated with one new message", wantBudget: MaxOutputBudget, truncateFirstTurn: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan []string, 8)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					MaxTokens int `json:"max_tokens"`
					Messages  []struct{ Content string }
				}
				var batch []Input
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 ||
					json.Unmarshal([]byte(body.Messages[1].Content), &batch) != nil {
					t.Error("invalid review request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if body.MaxTokens != test.wantBudget {
					t.Errorf("requested max_tokens = %d, want %d", body.MaxTokens, test.wantBudget)
				}
				ids := make([]string, 0, len(batch))
				for _, message := range batch {
					ids = append(ids, message.ID)
				}
				select {
				case requests <- ids:
				default:
					t.Error("too many review requests")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(batch) > 2 || (test.truncateFirstTurn && slices.Contains(ids, "a1")) {
					io.WriteString(w, `{"choices":[{"finish_reason":"length","message":{"content":"{\"topics\":["}}]}`)
					return
				}
				io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"topics\":[]}"}}]}`)
			}))
			config := Config{Provider: "gateway", Model: "review-test", BaseURL: gateway.URL, OutputBudget: MaxOutputBudget}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var checkpoints []int
			err := Review(ctx, config, Model{MaxOutputTokens: test.modelLimit}, messages, 0, nil, func(consumed int, _ []Topic) error {
				checkpoints = append(checkpoints, consumed)
				return nil
			})
			gateway.Close()
			close(requests)
			var batches [][]string
			for ids := range requests {
				batches = append(batches, ids)
			}
			wantBatches := [][]string{{"u1", "a1", "u2", "a2"}, {"u1", "a1"}, {"u2", "a2"}}
			wantCheckpoints := []int{2, 4}
			if test.truncateFirstTurn {
				wantBatches = [][]string{{"u1", "a1", "u2", "a2"}, {"u1", "a1"}, {"u1"}, {"u1", "a1"}}
				wantCheckpoints = []int{1}
				if err == nil || !strings.Contains(err.Error(), "请求上限 32768 token") {
					t.Errorf("expected the effective budget in the truncation error, got %v", err)
				}
			} else if err != nil {
				t.Errorf("expected split batches to succeed, got %v", err)
			}
			if !reflect.DeepEqual(batches, wantBatches) || !slices.Equal(checkpoints, wantCheckpoints) {
				t.Fatalf("batches=%v, checkpoints=%v, err=%v", batches, checkpoints, err)
			}
		})
	}
}

func TestReasoningEffortPayloads(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		baseURL  string
		effort   string
		check    func(t *testing.T, body map[string]any)
		reply    string
	}{
		{
			name: "openai chat low", provider: "openai", effort: "low",
			reply: `{"choices":[{"message":{"content":"OK"}}]}`,
			check: func(t *testing.T, body map[string]any) {
				if body["reasoning_effort"] != "low" {
					t.Fatalf("expected reasoning_effort=low, got %v", body)
				}
			},
		},
		{
			name: "responses medium", provider: "responses", baseURL: "https://api.openai.com/v1", effort: "medium",
			reply: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`,
			check: func(t *testing.T, body map[string]any) {
				reasoning, _ := body["reasoning"].(map[string]any)
				if reasoning["effort"] != "medium" {
					t.Fatalf("expected reasoning.effort=medium, got %v", body)
				}
			},
		},
		{
			name: "anthropic high", provider: "anthropic", effort: "high",
			reply: `{"stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`,
			check: func(t *testing.T, body map[string]any) {
				thinking, _ := body["thinking"].(map[string]any)
				if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(4096) || body["max_tokens"] != float64(4096+256) {
					t.Fatalf("unexpected anthropic thinking payload: %v", body)
				}
			},
		},
		{
			name: "anthropic none", provider: "anthropic", effort: "none",
			reply: `{"stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`,
			check: func(t *testing.T, body map[string]any) {
				thinking, _ := body["thinking"].(map[string]any)
				if thinking["type"] != "disabled" || body["max_tokens"] != float64(256) {
					t.Fatalf("unexpected anthropic disabled thinking payload: %v", body)
				}
			},
		},
		{
			name: "gemini none", provider: "gemini", effort: "none",
			reply: `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"OK"}]}}]}`,
			check: func(t *testing.T, body map[string]any) {
				gen, _ := body["generationConfig"].(map[string]any)
				tc, _ := gen["thinkingConfig"].(map[string]any)
				if tc["thinkingBudget"] != float64(0) {
					t.Fatalf("unexpected gemini thinkingConfig: %v", body)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			transport := testTransport(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				tc.check(t, body)
				return mockResponse(http.StatusOK, tc.reply), nil
			})
			cfg := Config{Provider: tc.provider, Model: "test-model", BaseURL: tc.baseURL, APIKey: fakeKey, ReasoningEffort: tc.effort}
			if _, err := requestText(context.Background(), cfg, "sys", "hi", 256, transport); err != nil {
				t.Fatalf("requestText failed: %v", err)
			}
		})
	}
}
