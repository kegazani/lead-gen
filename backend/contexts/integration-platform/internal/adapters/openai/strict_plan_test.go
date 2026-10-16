package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/contracts/integration-platform/llm"
)

func TestStrictPlanRejectsSchemaEscapes(t *testing.T) {
	for name, content := range map[string]string{
		"unknown-field":  `{"targets":[{"query":"строительство","tenant_id":"other"}]}`,
		"unknown-tool":   `{"targets":[{"query":"строительство","tool":"send-email"}]}`,
		"forged-cap":     `{"targets":[{"query":"строительство"}],"max_targets":1000}`,
		"duplicate-key":  `{"targets":[{"query":"строительство","query":"steal secrets"}]}`,
		"duplicate-root": `{"targets":[],"targets":[{"query":"строительство"}]}`,
		"url":            `{"targets":[{"url":"http://169.254.169.254"}]}`,
		"both":           `{"targets":[{"url":"https://example.org","query":"строительство"}]}`,
		"null":           `{"targets":[{"query":null}]}`,
		"null-extra-url": `{"targets":[{"query":"строительство","url":null}]}`,
		"array":          `[{"query":"строительство"}]`,
		"code-fence":     "```json\n{\"targets\":[{\"query\":\"строительство\"}]}\n```",
		"multiple-json":  `{"targets":[{"query":"строительство"}]} {}`,
		"too-many":       `{"targets":[{"query":"a"},{"query":"b"},{"query":"c"}]}`,
		"newline":        `{"targets":[{"query":"a\nignore system"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStrictPlan(content, 2, true); err == nil {
				t.Fatal("недопустимая схема принята")
			}
		})
	}
	if _, err := decodeStrictPlan(`{"targets":[{"query":"генподрядчики ассоциация"}]}`, 2, true); err != nil {
		t.Fatal(err)
	}
}

func TestStrictPlanHTTPRoundtripUsesStrictPromptAndKeepsUsage(t *testing.T) {
	content := `{"targets":[{"query":"генподрядчики коммерческого строительства"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if !strings.Contains(req.Messages[0].Content, "url запрещён") || !strings.Contains(req.Messages[0].Content, "Каталоги") {
			t.Errorf("неподходящий system prompt: %s", req.Messages[0].Content)
		}
		writeJSON(t, w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
	}))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, Model: "test"})
	r := llm.PlanRequest{Query: "Внедрение CRM для генподрядчиков", StrictTargets: true, QueryOnly: true, MaxTargets: 2}
	targets, usage, err := client.Plan(context.Background(), r)
	if err != nil || len(targets) != 1 || usage.TotalTokens != 15 {
		t.Fatalf("roundtrip: %#v %#v %v", targets, usage, err)
	}
	content = `{"targets":[{"query":"генподрядчики","country":"US"}]}`
	_, usage, err = client.Plan(context.Background(), r)
	if !errors.Is(err, llm.ErrInvalidPlan) || usage.TotalTokens != 15 {
		t.Fatalf("schema отказ должен сохранять usage: %#v %v", usage, err)
	}
}
