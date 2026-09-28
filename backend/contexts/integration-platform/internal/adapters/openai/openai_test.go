package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"lead-gen/contracts/integration-platform/contracttest"
	"lead-gen/contracts/integration-platform/llm"
)

// newTestServer поднимает локальный httptest-сервер, отвечающий на chat
// completions детерминированным JSON-планом. Авторизация не проверяется.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"targets":[{"query":"строительные компании в Казани"},{"url":"https://example.com/about"}]}`,
					},
				},
			},
		})
	})
	return httptest.NewServer(mux)
}

// writeJSON сериализует ответ и падает тест при ошибке.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("запись ответа: %v", err)
	}
}

// TestLLMPortContract прогоняет contract suite порта llm против адаптера OpenAI,
// работающего с локальным httptest-сервером.
func TestLLMPortContract(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	contracttest.RunLLMPortContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestLLMSuggestContract прогоняет contract suite операции Suggest порта llm
// против адаптера OpenAI, работающего с локальным httptest-сервером, который
// возвращает JSON-массив предложений.
func TestLLMSuggestContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `[{"sector":"Логистика","niche":"Сборные грузы","seed_queries":["перевозки сборных грузов"]},{"sector":"Маркетинг","niche":"Агентства","seed_queries":["b2b маркетинговые агентства"]}]`,
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	contracttest.RunLLMSuggestContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestPlanReturnsUsage проверяет, что адаптер возвращает расход токенов из поля
// usage ответа провайдера.
func TestPlanReturnsUsage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `[{"query":"строительные компании"}]`}},
			},
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 34,
				"total_tokens":      46,
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	targets, usage, err := client.Plan(context.Background(), llm.PlanRequest{Query: "строительные компании"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("число пунктов плана %d, ожидалось 1", len(targets))
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 34 || usage.TotalTokens != 46 {
		t.Fatalf("usage: получено %+v, ожидалось prompt=12 completion=34 total=46", usage)
	}
}

// TestPlanWithoutUsage проверяет, что отсутствие поля usage даёт нулевой расход, а
// не ошибку.
func TestPlanWithoutUsage(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	_, usage, err := client.Plan(context.Background(), llm.PlanRequest{Query: "строительные компании"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if usage != (llm.Usage{}) {
		t.Fatalf("usage без поля в ответе: получено %+v, ожидался нулевой", usage)
	}
}

// TestPlanParsesTargetsObject проверяет основной формат ответа модели: JSON-объект
// с массивом targets.
func TestPlanParsesTargetsObject(t *testing.T) {
	srv := newChatTestServer(t, `{"targets":[{"query":"строительные компании в Казани"},{"url":"https://example.com/about"}]}`)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	targets, _, err := client.Plan(context.Background(), llm.PlanRequest{Query: "строительные компании"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertPlanTargets(t, targets)
}

// TestPlanFallsBackToBareArray проверяет терпимость парсера: «голый» массив целей
// даёт тот же результат, что и объект targets.
func TestPlanFallsBackToBareArray(t *testing.T) {
	srv := newChatTestServer(t, "Вот план.\n```json\n"+`[{"query":"строительные компании в Казани"},{"url":"https://example.com/about"}]`+"\n```")
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	targets, _, err := client.Plan(context.Background(), llm.PlanRequest{Query: "строительные компании"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertPlanTargets(t, targets)
}

// assertPlanTargets сверяет цели с общим ожиданием двух тестов формата плана.
func assertPlanTargets(t *testing.T, targets []llm.Target) {
	t.Helper()
	if len(targets) != 2 {
		t.Fatalf("число целей %d, ожидалось 2: %+v", len(targets), targets)
	}
	if targets[0].Query != "строительные компании в Казани" || targets[0].URL != "" {
		t.Fatalf("цель 0 = %+v, ожидался query", targets[0])
	}
	if targets[1].URL != "https://example.com/about" || targets[1].Query != "" {
		t.Fatalf("цель 1 = %+v, ожидался url", targets[1])
	}
}

// TestPlanSendsInterpretation проверяет, что критерии релевантности (целевой лид,
// признаки потребности и исключения) попадают в тело запроса к модели.
func TestPlanSendsInterpretation(t *testing.T) {
	var captured chatRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("чтение тела запроса: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"targets":[{"query":"производители мебели"}]}`}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	interpretation := llm.Interpretation{
		TargetLead:  "производители мебели",
		NeedSignals: []string{"ищут поставщика фурнитуры"},
		Exclusions:  []string{"информационные порталы", "каталоги", "магазины", "отдельные статьи"},
	}
	if _, _, err := client.Plan(context.Background(), llm.PlanRequest{Query: "мебельные фабрики", Interpretation: interpretation}); err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(captured.Messages) != 2 {
		t.Fatalf("сообщений %d, ожидалось 2", len(captured.Messages))
	}
	user := captured.Messages[1].Content
	for _, want := range []string{"производители мебели", "ищут поставщика фурнитуры", "информационные порталы", "каталоги", "магазины", "отдельные статьи"} {
		if !strings.Contains(user, want) {
			t.Fatalf("в пользовательском сообщении нет %q:\n%s", want, user)
		}
	}
	if captured.ResponseFormat == nil || captured.ResponseFormat.Type != "json_object" {
		t.Fatalf("response_format = %+v, ожидался json_object", captured.ResponseFormat)
	}
}

// TestPlanUncertaintyReliesOnQuery проверяет, что при явной неопределённости
// интерпретации промпт говорит об отсутствии критериев и опоре на запрос.
func TestPlanUncertaintyReliesOnQuery(t *testing.T) {
	var captured chatRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("чтение тела запроса: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"targets":[{"query":"мебельные фабрики"}]}`}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, _, err := client.Plan(context.Background(), llm.PlanRequest{Query: "мебельные фабрики", Interpretation: llm.Interpretation{Uncertainty: true}}); err != nil {
		t.Fatalf("Plan: %v", err)
	}

	user := captured.Messages[1].Content
	if !strings.Contains(user, "не определены") {
		t.Fatalf("при неопределённости промпт должен сообщать об отсутствии критериев:\n%s", user)
	}
	if !strings.Contains(user, "Запрос: мебельные фабрики") {
		t.Fatalf("промпт должен содержать текст запроса:\n%s", user)
	}
}

// TestLLMConverseContract прогоняет contract suite операции Converse порта llm,
// против адаптера OpenAI, работающего с локальным httptest-сервером, который
// возвращает JSON-объект реплики.
func TestLLMConverseContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"text":"Какой регион нужен?","targets":[],"ready":false}`,
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	contracttest.RunLLMConverseContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestConverseParsesReply проверяет, что адаптер разбирает реплику, черновик цели
// и признак готовности, а также возвращает расход токенов.
func TestConverseParsesReply(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role": "assistant",
						"content": `Вот черновик плана.\n` + "```json" + `
{"text":"План готов","targets":[{"query":"логистические компании"},{"url":"https://example.com/contacts"},{}],"ready":true}
` + "```",
					},
				},
			},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "ищем логистов"},
		{Role: llm.RoleAssistant, Content: "какой регион?"},
		{Role: llm.RoleUser, Content: "Россия"},
	}
	reply, usage, err := client.Converse(context.Background(), llm.ConverseRequest{History: history})
	if err != nil {
		t.Fatalf("Converse: %v", err)
	}
	if reply.Text != "План готов" {
		t.Fatalf("text = %q, ожидалось %q", reply.Text, "План готов")
	}
	if !reply.Ready {
		t.Fatal("ожидался ready=true")
	}
	if len(reply.Targets) != 2 {
		t.Fatalf("черновик целей: %d, ожидалось 2 (неполная цель отбрасывается)", len(reply.Targets))
	}
	if usage.TotalTokens != 12 {
		t.Fatalf("usage.TotalTokens = %d, ожидалось 12", usage.TotalTokens)
	}
}

// newStreamTestServer поднимает httptest-сервер, отдающий потоковый ответ в
// формате SSE: сначала текст реплики (по фрагментам, включая разделитель
// ===PLAN===, разрезанный между кадрами), затем JSON-хвост плана и учёт токенов.
func newStreamTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(delta string) {
			frame, err := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": delta}}},
			})
			if err != nil {
				t.Fatalf("кадр потока: %v", err)
			}
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("Какой ")
		write("регион ")
		write("нужен?\n===PLAN")
		write("===\n")
		write(`{"targets":[{"query":"логистические компании"}],"limits":{"max_depth":2,"max_time_seconds":120},"ready":true}`)
		usageFrame, err := json.Marshal(map[string]any{
			"choices": []any{},
			"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
		if err != nil {
			t.Fatalf("кадр usage: %v", err)
		}
		fmt.Fprintf(w, "data: %s\n\n", usageFrame)
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return httptest.NewServer(mux)
}

// TestLLMConverseStreamContract прогоняет contract suite потоковой операции против
// адаптера OpenAI с потоковым httptest-сервером.
func TestLLMConverseStreamContract(t *testing.T) {
	srv := newStreamTestServer(t)
	defer srv.Close()

	contracttest.RunLLMConverseStreamContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestConverseStreamEmitsDeltasAndFinal проверяет, что поток отдаёт фрагменты
// реплики до разделителя, а завершающий кадр несёт цели, лимиты, готовность и
// расход токенов.
func TestConverseStreamEmitsDeltasAndFinal(t *testing.T) {
	srv := newStreamTestServer(t)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	history := []llm.Message{{Role: llm.RoleUser, Content: "ищем логистов"}}

	var text string
	var final *llm.ConverseReply
	var usage llm.Usage
	var deltas int
	for delta, err := range client.ConverseStream(context.Background(), llm.ConverseRequest{History: history}) {
		if err != nil {
			t.Fatalf("ConverseStream: %v", err)
		}
		if delta.Text != "" {
			deltas++
			text += delta.Text
		}
		if delta.Reply != nil {
			final = delta.Reply
			usage = delta.Usage
		}
	}

	if text != "Какой регион нужен?" {
		t.Fatalf("склеенный текст = %q, ожидалось %q", text, "Какой регион нужен?")
	}
	if deltas < 2 {
		t.Fatalf("фрагментов текста %d, ожидалось несколько (поток должен отдавать по частям)", deltas)
	}
	if final == nil {
		t.Fatal("поток не завершился кадром с ответом")
	}
	if final.Text != "Какой регион нужен?" {
		t.Fatalf("текст завершающего кадра = %q", final.Text)
	}
	if !final.Ready {
		t.Fatal("ожидался ready=true")
	}
	if len(final.Targets) != 1 || final.Targets[0].Query != "логистические компании" {
		t.Fatalf("цели завершающего кадра: %+v", final.Targets)
	}
	if final.Limits.MaxDepth != 2 || final.Limits.MaxTimeSeconds != 120 {
		t.Fatalf("лимиты завершающего кадра: %+v", final.Limits)
	}
	if usage.TotalTokens != 7 {
		t.Fatalf("usage.TotalTokens = %d, ожидалось 7", usage.TotalTokens)
	}
}

// newDeltaStreamTestServer поднимает httptest-сервер, отдающий потоковый ответ в
// формате SSE по заданным фрагментам текста (каждый — отдельный кадр), без
// хвоста плана. Нужен для проверки целостности UTF-8 на коротких дельтах.
func newDeltaStreamTestServer(t *testing.T, deltas ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, delta := range deltas {
			frame, err := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": delta}}},
			})
			if err != nil {
				t.Fatalf("кадр потока: %v", err)
			}
			fmt.Fprintf(w, "data: %s\n\n", frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return httptest.NewServer(mux)
}

// TestConverseStreamKeepsUTF8Intact проверяет, что поток короткими дельтами по
// одному символу кириллицы не разрывает многобайтовые символы: каждый фрагмент —
// валидный UTF-8 без U+FFFD, а их склейка равна полной реплике.
func TestConverseStreamKeepsUTF8Intact(t *testing.T) {
	const reply = "Привет, мир"

	runes := []rune(reply)
	deltas := make([]string, 0, len(runes))
	for _, r := range runes {
		deltas = append(deltas, string(r))
	}
	srv := newDeltaStreamTestServer(t, deltas...)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	history := []llm.Message{{Role: llm.RoleUser, Content: "поздоровайся"}}

	var text string
	var final *llm.ConverseReply
	for delta, err := range client.ConverseStream(context.Background(), llm.ConverseRequest{History: history}) {
		if err != nil {
			t.Fatalf("ConverseStream: %v", err)
		}
		if !utf8.ValidString(delta.Text) {
			t.Fatalf("фрагмент не является валидным UTF-8: %q", delta.Text)
		}
		if strings.ContainsRune(delta.Text, utf8.RuneError) {
			t.Fatalf("фрагмент содержит U+FFFD (разрыв символа): %q", delta.Text)
		}
		text += delta.Text
		if delta.Reply != nil {
			final = delta.Reply
		}
	}

	if text != reply {
		t.Fatalf("склеенный текст = %q, ожидалось %q", text, reply)
	}
	if !utf8.ValidString(text) {
		t.Fatalf("склеенный текст не является валидным UTF-8: %q", text)
	}
	if final == nil {
		t.Fatal("поток не завершился кадром с ответом")
	}
	if final.Text != reply {
		t.Fatalf("текст завершающего кадра = %q, ожидалось %q", final.Text, reply)
	}
}

// TestConverseStreamSentinelAfterMultibyte проверяет, что разделитель плана,
// пришедший сразу после многобайтового символа, не искажает текст реплики перед
// ним и корректно разбирается в завершающий кадр.
func TestConverseStreamSentinelAfterMultibyte(t *testing.T) {
	// Каждый символ реплики — отдельный кадр, затем разделитель, разрезанный
	// между кадрами, перенос строки и JSON-хвост плана.
	deltas := []string{"Г", "о", "т", "о", "в", "о", "===PLAN", "===", "\n",
		`{"targets":[{"query":"логистические компании"}],"limits":{"max_depth":2,"max_time_seconds":120},"ready":true}`}
	srv := newDeltaStreamTestServer(t, deltas...)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	history := []llm.Message{{Role: llm.RoleUser, Content: "собери план"}}

	var text string
	var final *llm.ConverseReply
	for delta, err := range client.ConverseStream(context.Background(), llm.ConverseRequest{History: history}) {
		if err != nil {
			t.Fatalf("ConverseStream: %v", err)
		}
		if !utf8.ValidString(delta.Text) {
			t.Fatalf("фрагмент не является валидным UTF-8: %q", delta.Text)
		}
		text += delta.Text
		if delta.Reply != nil {
			final = delta.Reply
		}
	}

	if text != "Готово" {
		t.Fatalf("склеенный текст = %q, ожидалось %q", text, "Готово")
	}
	if final == nil {
		t.Fatal("поток не завершился кадром с ответом")
	}
	if final.Text != "Готово" {
		t.Fatalf("текст завершающего кадра = %q, ожидалось %q", final.Text, "Готово")
	}
	if !final.Ready {
		t.Fatal("ожидался ready=true")
	}
	if len(final.Targets) != 1 || final.Targets[0].Query != "логистические компании" {
		t.Fatalf("цели завершающего кадра: %+v", final.Targets)
	}
	if final.Limits.MaxDepth != 2 || final.Limits.MaxTimeSeconds != 120 {
		t.Fatalf("лимиты завершающего кадра: %+v", final.Limits)
	}
}

// newChatTestServer поднимает httptest-сервер, отдающий заданный content как
// ответ chat completion.
func newChatTestServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": content}},
			},
		})
	})
	return httptest.NewServer(mux)
}

// TestLLMInterpretContract прогоняет contract suite операции Interpret против
// адаптера OpenAI, работающего с локальным httptest-сервером, который возвращает
// JSON-объект критериев релевантности.
func TestLLMInterpretContract(t *testing.T) {
	srv := newChatTestServer(t, `{"target_lead":"логистические компании","need_signals":["нужна автоматизация логистики"],"exclusions":["физлица"],"hypothesis_signals":["отрасль совпадает"],"uncertainty":false}`)
	defer srv.Close()

	contracttest.RunLLMInterpretContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestLLMAssessContract прогоняет contract suite операции Assess против адаптера
// OpenAI, работающего с локальным httptest-сервером, который возвращает
// JSON-объект оценки.
func TestLLMAssessContract(t *testing.T) {
	srv := newChatTestServer(t, `{"verdict":"fits","signal_kind":"direct","evidence":[{"criterion":"признак потребности","fact":"компания описала задачу","source_url":"https://example.com/news","reasoning":"задача совпадает с потребностью","signal_kind":"direct"}]}`)
	defer srv.Close()

	contracttest.RunLLMAssessContract(t, func(t *testing.T) llm.Port {
		t.Helper()
		return New(Config{BaseURL: srv.URL})
	})
}

// TestInterpretParsesCriteria проверяет, что адаптер разбирает критерии из ответа
// модели, в том числе обёрнутого в markdown-ограждение, и отбрасывает пустые
// строки списков.
func TestInterpretParsesCriteria(t *testing.T) {
	srv := newChatTestServer(t, "Вот критерии.\n```json\n"+`{"target_lead":"логистические компании","need_signals":[" нужна автоматизация ",""],"exclusions":["физлица"],"hypothesis_signals":[],"uncertainty":false}`+"\n```")
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Interpret(context.Background(), llm.InterpretRequest{Query: "ищем логистов"})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if got.TargetLead != "логистические компании" {
		t.Fatalf("target_lead = %q", got.TargetLead)
	}
	if len(got.NeedSignals) != 1 || got.NeedSignals[0] != "нужна автоматизация" {
		t.Fatalf("need_signals = %v, ожидалось очищенное значение", got.NeedSignals)
	}
	if len(got.Exclusions) != 1 || got.Exclusions[0] != "физлица" {
		t.Fatalf("exclusions = %v", got.Exclusions)
	}
	if got.Uncertainty {
		t.Fatal("ожидалось uncertainty=false")
	}
}

// TestInterpretAllowsUncertainty проверяет, что явная неопределённость — валидный
// ответ интерпретации без выдуманных критериев.
func TestInterpretAllowsUncertainty(t *testing.T) {
	srv := newChatTestServer(t, `{"target_lead":"","need_signals":[],"uncertainty":true}`)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Interpret(context.Background(), llm.InterpretRequest{Query: "непонятно, что ищем"})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if !got.Uncertainty {
		t.Fatal("ожидалась явная неопределённость")
	}
}

// TestAssessParsesFitsWithUsage проверяет разбор оценки «Подходит» с косвенным
// свидетельством и расход токенов.
func TestAssessParsesFitsWithUsage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"verdict":"fits","signal_kind":"indirect","evidence":[{"criterion":"c","fact":"f","source_url":"https://example.com/x","reasoning":"r","signal_kind":"indirect"}]}`}},
			},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, usage, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != llm.VerdictFits || got.SignalKind != llm.SignalIndirect {
		t.Fatalf("вердикт/сигнал = %q/%q, ожидалось fits/indirect", got.Verdict, got.SignalKind)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].SourceURL != "https://example.com/x" {
		t.Fatalf("свидетельства: %+v", got.Evidence)
	}
	if usage.TotalTokens != 7 {
		t.Fatalf("usage.TotalTokens = %d, ожидалось 7", usage.TotalTokens)
	}
}

// TestAssessParsesHypothesisWithMissing проверяет, что оценка «Гипотеза» несёт
// недостающее свидетельство.
func TestAssessParsesHypothesisWithMissing(t *testing.T) {
	srv := newChatTestServer(t, `{"verdict":"hypothesis","signal_kind":"profile","evidence":[{"criterion":"профиль","fact":"отрасль совпадает","source_url":"https://example.com/about","reasoning":"нужная отрасль","signal_kind":"profile","missing":"нет заявления о потребности"}]}`)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != llm.VerdictHypothesis || got.SignalKind != llm.SignalProfile {
		t.Fatalf("вердикт/сигнал = %q/%q, ожидалось hypothesis/profile", got.Verdict, got.SignalKind)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Missing == "" {
		t.Fatalf("ожидалось недостающее свидетельство: %+v", got.Evidence)
	}
}

// TestAssessParsesUnsuitableWithMismatch проверяет, что «Не подходит» несёт
// установленное несоответствие.
func TestAssessParsesUnsuitableWithMismatch(t *testing.T) {
	srv := newChatTestServer(t, `{"verdict":"unsuitable","signal_kind":"profile","evidence":[{"criterion":"исключение","fact":"компания работает с физлицами","source_url":"https://example.com/services","reasoning":"не тот тип клиента","signal_kind":"profile","mismatch":"обслуживает только физлиц"}]}`)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != llm.VerdictUnsuitable {
		t.Fatalf("verdict = %q, ожидалось unsuitable", got.Verdict)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Mismatch == "" {
		t.Fatalf("ожидалось несоответствие: %+v", got.Evidence)
	}
}

// TestAssessRejectsInvalidResponse проверяет, что невалидный ответ модели
// (например, «fits» без свидетельств) возвращается обычной ошибкой Go.
func TestAssessRejectsInvalidResponse(t *testing.T) {
	srv := newChatTestServer(t, `{"verdict":"fits","signal_kind":"profile","evidence":[]}`)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, _, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"}); err == nil {
		t.Fatal("невалидная оценка модели должна быть отвергнута")
	}
}

// TestAssessRepairsIncompleteResponse проверяет, что при структурно неполном
// первом ответе адаптер делает ровно одну повторную попытку и возвращает валидную
// оценку из второго ответа.
func TestAssessRepairsIncompleteResponse(t *testing.T) {
	var bodies []chatRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("чтение тела запроса: %v", err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			writeJSON(t, w, map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": `{"verdict":"fits","signal_kind":"profile","evidence":[]}`}},
				},
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"verdict":"fits","signal_kind":"direct","evidence":[{"criterion":"c","fact":"f","source_url":"https://example.com/x","reasoning":"r","signal_kind":"direct"}]}`}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != llm.VerdictFits || got.SignalKind != llm.SignalDirect {
		t.Fatalf("вердикт/сигнал = %q/%q, ожидалось fits/direct", got.Verdict, got.SignalKind)
	}
	if len(bodies) != 2 {
		t.Fatalf("запросов к модели %d, ожидалось 2", len(bodies))
	}
	if len(bodies[1].Messages) != 4 {
		t.Fatalf("сообщений в repair-запросе %d, ожидалось 4", len(bodies[1].Messages))
	}
	if bodies[1].Messages[2].Role != "assistant" || !strings.Contains(bodies[1].Messages[2].Content, `"verdict":"fits"`) {
		t.Fatalf("assistant-сообщение repair = %+v", bodies[1].Messages[2])
	}
	if bodies[1].Messages[3].Role != "user" || !strings.Contains(bodies[1].Messages[3].Content, "не прошёл проверку") {
		t.Fatalf("user-сообщение repair = %+v", bodies[1].Messages[3])
	}
}

// TestAssessFailsAfterRepair проверяет, что два структурно неполных ответа дают
// явную ошибку, а не частичную оценку.
func TestAssessFailsAfterRepair(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		writeJSON(t, w, map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"verdict":"unsuitable","signal_kind":"profile","evidence":[]}`}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	got, _, err := client.Assess(context.Background(), llm.AssessRequest{JobID: "job-1"})
	if err == nil {
		t.Fatalf("ожидалась ошибка после провала repair, получена оценка %+v", got)
	}
	if calls != 2 {
		t.Fatalf("вызовов модели %d, ожидалось 2 (ровно одна повторная попытка)", calls)
	}
}
