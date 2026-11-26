package distill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

const (
	DefaultInputBudget  = 24000
	DefaultOutputBudget = 4096
	MinInputBudget      = 2048
	MaxInputBudget      = 262144
	MinOutputBudget     = 256
	MaxOutputBudget     = 32768
)

type Plan struct {
	InputBudget       int    `json:"inputBudget"`
	OutputBudget      int    `json:"outputBudget"`
	ThinkingBudget    int    `json:"thinkingBudget,omitempty"`
	TotalOutputBudget int    `json:"totalOutputBudget"`
	EstimatedInput    int    `json:"estimatedInput"`
	Included          int    `json:"included"`
	Total             int    `json:"total"`
	Shortened         bool   `json:"shortened"`
	ContextWindow     int    `json:"contextWindow,omitempty"`
	Confirmation      string `json:"confirmation"`
	transcript        string
	refs              map[string]int
}

func Budgets(input, output int) (int, int, error) {
	if input == 0 {
		input = DefaultInputBudget
	}
	if output == 0 {
		output = DefaultOutputBudget
	}
	if input < MinInputBudget || input > MaxInputBudget || output < MinOutputBudget || output > MaxOutputBudget {
		return 0, 0, errors.New("输入预算需在 2048–262144、输出上限需在 256–32768 token 之间")
	}
	return input, output, nil
}

// Prepare fits the latest messages into a single request, preserving their original IDs and order.
func Prepare(config Config, model Model, messages []Input) (Plan, error) {
	return prepare(config, model, messages, instructions)
}

func fitBudgets(config Config, model Model, input, output int) (int, int, error) {
	thinking := anthropicThinkingBudget(config)
	if model.MaxOutputTokens > 0 {
		output = min(output, model.MaxOutputTokens-thinking)
	}
	if model.ContextWindow > 0 {
		output = min(output, max(1, model.ContextWindow-MinInputBudget)-thinking)
		input = min(input, model.ContextWindow-output-thinking)
	}
	if output < 1 {
		return 0, 0, errors.New("模型的输出上限不足以容纳推理预算，请降低推理强度或更换模型")
	}
	return input, output, nil
}

func prepare(config Config, model Model, messages []Input, prompt string) (Plan, error) {
	input, output, err := Budgets(config.InputBudget, config.OutputBudget)
	if err != nil {
		return Plan{}, err
	}
	input, output, err = fitBudgets(config, model, input, output)
	if err != nil {
		return Plan{}, err
	}
	refs := messageIDs(messages)
	if len(refs) == 0 {
		return Plan{}, errors.New("distill: no messages with reference IDs")
	}
	for _, count := range refs {
		if count > 1 {
			return Plan{}, errors.New("distill: duplicate message reference ID")
		}
	}

	// shortcut: use one token per UTF-8 byte plus framing until model-specific tokenizers are available.
	used := len(prompt) + 128 + 2
	selected := make([]string, 0, len(messages))
	includedRefs := make(map[string]int)
	shortened := false
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if refs[message.ID] != 1 || strings.TrimSpace(message.Text) == "" {
			continue
		}
		comma := min(len(selected), 1)
		remaining := input - used - comma
		var encoded string
		if len(message.Text) <= remaining {
			raw, _ := json.Marshal(message)
			encoded = string(raw)
		}
		if encoded == "" || len(encoded) > remaining {
			if len(selected) != 0 {
				break
			}
			encoded = shorten(message, remaining)
			if encoded == "" {
				break
			}
			shortened = true
		}
		selected = append(selected, encoded)
		includedRefs[message.ID] = 1
		used += len(encoded) + comma
	}
	if len(selected) == 0 {
		return Plan{}, errors.New("输入预算不足以容纳提炼提示和会话消息，请提高预算或换用上下文更大的模型")
	}
	slices.Reverse(selected)
	transcript := "[" + strings.Join(selected, ",") + "]"
	thinking := anthropicThinkingBudget(config)
	plan := Plan{
		InputBudget: input, OutputBudget: output, EstimatedInput: used, Included: len(selected),
		ThinkingBudget: thinking, TotalOutputBudget: output + thinking,
		Total: len(messages), Shortened: shortened, ContextWindow: model.ContextWindow,
		transcript: transcript, refs: includedRefs,
	}
	confirmation, _ := json.Marshal([]any{config.Provider, config.BaseURL, config.Model, config.ReasoningEffort, input, plan.TotalOutputBudget, prompt, transcript, plan.Total})
	hash := sha256.Sum256(confirmation)
	plan.Confirmation = hex.EncodeToString(hash[:])
	return plan, nil
}

func shorten(message Input, limit int) string {
	// Only the head and tail can fit. Bound the search before converting large messages to runes.
	if limit < 1 {
		return ""
	}
	text := message.Text
	if len(text) > 2*limit {
		text = strings.ToValidUTF8(text[:limit], "") + strings.ToValidUTF8(text[len(text)-limit:], "")
	}
	runes := []rune(text)
	best := ""
	for low, high := 1, len(runes)-1; low <= high; {
		mid := (low + high) / 2
		head, tail := (mid+1)/2, mid/2
		message.Text = string(runes[:head]) + "\n[…]\n" + string(runes[len(runes)-tail:])
		encoded, _ := json.Marshal(message)
		if len(encoded) <= limit {
			best = string(encoded)
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best
}
