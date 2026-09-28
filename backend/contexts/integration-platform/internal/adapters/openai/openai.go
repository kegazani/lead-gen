// Package openai — адаптер OpenAI-совместимого чат-клиента (провайдер DeepSeek),
// реализующий порт llm.Port.
//
// Адаптер не тянет SDK поставщика: Plan формирует обычный chat completion
// запрос по HTTP и разбирает ответ модели в []llm.Target. Источник истины для
// полей — DTO контракта lead-gen/contracts, а не сырой JSON провайдера. Ошибки
// возвращаются обычными ошибками Go, без утечки типов HTTP/SDK наружу.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"unicode/utf8"

	"lead-gen/contracts/integration-platform/llm"
)

// defaultBaseURL — базовый адрес OpenAI-совместимого API провайдера DeepSeek.
const defaultBaseURL = "https://api.deepseek.com"

// defaultModel — модель по умолчанию.
const defaultModel = "deepseek-chat"

// Config — настройки адаптера OpenAI-совместимого клиента.
type Config struct {
	// BaseURL — базовый адрес API. Пустое значение заменяется на defaultBaseURL.
	BaseURL string
	// Model — идентификатор модели. Пустое значение заменяется на defaultModel.
	Model string
	// APIKey — ключ доступа. Пустой ключ допустим для тестов с локальным
	// httptest-сервером.
	APIKey string
	// HTTPClient — клиент для исходящих запросов. nil означает http.DefaultClient.
	HTTPClient *http.Client
}

// Client — HTTP-клиент OpenAI-совместимого провайдера, реализующий llm.Port.
type Client struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// Проверка на этапе компиляции, что Client реализует llm.Port.
var _ llm.Port = (*Client)(nil)

// New собирает клиент, применяя значения по умолчанию. Конструктор не валидирует
// ключ: валидация конфигурации — забота корневого пакета.
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{
		baseURL:    baseURL,
		model:      model,
		apiKey:     cfg.APIKey,
		httpClient: client,
	}
}

// Plan раскладывает запрос оператора в серию поисковых запросов и стартовых URL.
// Модель обязуется вернуть JSON-объект {"targets": [{query|url}, ...]}, который
// адаптер отображает на []llm.Target; «голый» массив принимается как фолбэк.
// Критерии релевантности запроса передаются в промпт и направляют план. Контекст
// уважается: отмена/дедлайн прерывают вызов.
func (c *Client) Plan(ctx context.Context, r llm.PlanRequest) ([]llm.Target, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return nil, llm.Usage{}, err
	}
	if err := r.Validate(); err != nil {
		return nil, llm.Usage{}, err
	}

	body := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: planSystemPrompt},
			{Role: "user", Content: planUserMessage(r)},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var response chatResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/chat/completions"), body, &response); err != nil {
		return nil, llm.Usage{}, err
	}
	if len(response.Choices) == 0 {
		return nil, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели (нет choices)")
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return nil, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели")
	}

	// Основной формат — объект {"targets": [...]}; extractJSONObject вырезает
	// объект из markdown-ограждения или пояснительного текста. Если модель всё же
	// вернула «голый» массив, применяем extractJSONArray как фолбэк — парсер
	// остаётся терпимым к обеим формам.
	var raw []targetJSON
	var plan planJSON
	if err := json.Unmarshal([]byte(extractJSONObject(content)), &plan); err == nil && plan.Targets != nil {
		raw = plan.Targets
	} else if err := json.Unmarshal([]byte(extractJSONArray(content)), &raw); err != nil {
		return nil, llm.Usage{}, fmt.Errorf("openai: разбор плана модели: %w", err)
	}

	targets := make([]llm.Target, 0, len(raw))
	for _, t := range raw {
		targets = append(targets, llm.Target{Query: t.Query, URL: t.URL})
	}
	return targets, usageFrom(response.Usage), nil
}

// Suggest подбирает по описанию бизнеса/задачи сектора, ниши и готовые
// seed-запросы. Модель обязуется вернуть JSON-массив объектов
// {sector, niche, seed_queries}, который адаптер отображает на []llm.Suggestion.
// Контекст уважается: отмена/дедлайн прерывают вызов.
func (c *Client) Suggest(ctx context.Context, r llm.SuggestRequest) ([]llm.Suggestion, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return nil, llm.Usage{}, err
	}
	if err := r.Validate(); err != nil {
		return nil, llm.Usage{}, err
	}

	body := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: suggestSystemPrompt},
			{Role: "user", Content: r.Description},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var response chatResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/chat/completions"), body, &response); err != nil {
		return nil, llm.Usage{}, err
	}
	if len(response.Choices) == 0 {
		return nil, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели (нет choices)")
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return nil, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели")
	}

	payload := extractJSONArray(content)

	var raw []suggestionJSON
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, llm.Usage{}, fmt.Errorf("openai: разбор предложений модели: %w", err)
	}

	suggestions := make([]llm.Suggestion, 0, len(raw))
	for _, s := range raw {
		suggestions = append(suggestions, llm.Suggestion{
			Sector:      s.Sector,
			Niche:       s.Niche,
			SeedQueries: s.SeedQueries,
		})
	}
	return suggestions, usageFrom(response.Usage), nil
}

// Converse выполняет один ход диалога: отправляет историю разговора модели и
// разбирает её ответ в llm.ConverseReply. Модель обязуется вернуть JSON-объект
// {text, targets, ready}, где targets — массив {query, url}. Неполные цели
// черновика отбрасываются, а пустая реплика — ошибка. Контекст уважается:
// отмена/дедлайн прерывают вызов.
func (c *Client) Converse(ctx context.Context, r llm.ConverseRequest) (llm.ConverseReply, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, err
	}
	if err := r.Validate(); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, err
	}

	messages := make([]chatMessage, 0, len(r.History)+1)
	messages = append(messages, chatMessage{Role: "system", Content: converseSystemPrompt})
	for _, m := range r.History {
		messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
	}
	body := chatRequest{
		Model:          c.model,
		Messages:       messages,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var response chatResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/chat/completions"), body, &response); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, err
	}
	if len(response.Choices) == 0 {
		return llm.ConverseReply{}, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели (нет choices)")
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return llm.ConverseReply{}, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели")
	}

	payload := extractJSONObject(content)

	var raw converseJSON
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, fmt.Errorf("openai: разбор реплики модели: %w", err)
	}

	reply := llm.ConverseReply{Text: strings.TrimSpace(raw.Text), Ready: raw.Ready, Limits: raw.Limits.toPlanLimits()}
	for _, t := range raw.Targets {
		target := llm.Target{Query: t.Query, URL: t.URL}
		if err := target.Validate(); err != nil {
			// Черновик может содержать неполную цель; такая цель не ломает ход.
			continue
		}
		reply.Targets = append(reply.Targets, target)
	}
	if err := reply.Validate(); err != nil {
		return llm.ConverseReply{}, llm.Usage{}, fmt.Errorf("openai: %w", err)
	}
	return reply, usageFrom(response.Usage), nil
}

// Interpret превращает бизнес-запрос оператора в критерии релевантности. Модель
// обязуется вернуть JSON-объект {target_lead, need_signals, exclusions,
// hypothesis_signals, uncertainty}, который адаптер отображает на
// llm.Interpretation. Контекст уважается: отмена/дедлайн прерывают вызов.
func (c *Client) Interpret(ctx context.Context, r llm.InterpretRequest) (llm.Interpretation, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Interpretation{}, llm.Usage{}, err
	}
	if err := r.Validate(); err != nil {
		return llm.Interpretation{}, llm.Usage{}, err
	}

	body := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: interpretSystemPrompt},
			{Role: "user", Content: r.Query},
		},
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var response chatResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/chat/completions"), body, &response); err != nil {
		return llm.Interpretation{}, llm.Usage{}, err
	}
	if len(response.Choices) == 0 {
		return llm.Interpretation{}, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели (нет choices)")
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return llm.Interpretation{}, llm.Usage{}, fmt.Errorf("openai: пустой ответ модели")
	}

	payload := extractJSONObject(content)

	var raw interpretationJSON
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return llm.Interpretation{}, llm.Usage{}, fmt.Errorf("openai: разбор интерпретации модели: %w", err)
	}

	interpretation := raw.toInterpretation()
	if err := interpretation.Validate(); err != nil {
		return llm.Interpretation{}, llm.Usage{}, fmt.Errorf("openai: %w", err)
	}
	return interpretation, usageFrom(response.Usage), nil
}

// Assess оценивает компанию относительно критериев релевантности задания. Модель
// обязуется вернуть JSON-объект {verdict, signal_kind, evidence}, который адаптер
// отображает на llm.Assessment. Если первый ответ структурно неполон, адаптер
// делает ровно одну повторную попытку, показывая модели её невалидный ответ и
// недостающие поля; при повторной неудаче возвращается явная ошибка, а не
// частичная оценка. Контекст уважается: отмена/дедлайн прерывают вызов.
func (c *Client) Assess(ctx context.Context, r llm.AssessRequest) (llm.Assessment, llm.Usage, error) {
	if err := ctx.Err(); err != nil {
		return llm.Assessment{}, llm.Usage{}, err
	}
	if err := r.Validate(); err != nil {
		return llm.Assessment{}, llm.Usage{}, err
	}

	messages := []chatMessage{
		{Role: "system", Content: assessSystemPrompt},
		{Role: "user", Content: assessUserMessage(r)},
	}

	first := c.assessOnce(ctx, messages)
	if first.err == nil {
		return first.assessment, first.usage, nil
	}
	if !first.repairable {
		// Ошибка транспорта или пустой ответ: повтор бессмысленен.
		return llm.Assessment{}, llm.Usage{}, first.err
	}

	// Repair: ровно одна повторная попытка — те же системный промпт и материал,
	// плюс невалидный ответ модели и требование дозаполнить обязательные поля.
	repair := make([]chatMessage, 0, len(messages)+2)
	repair = append(repair, messages...)
	repair = append(repair,
		chatMessage{Role: "assistant", Content: first.content},
		chatMessage{Role: "user", Content: assessRepairMessage(first.err)},
	)

	second := c.assessOnce(ctx, repair)
	if second.err != nil {
		return llm.Assessment{}, llm.Usage{}, fmt.Errorf("openai: оценка не прошла проверку после повторного вывода: %w", second.err)
	}
	return second.assessment, second.usage, nil
}

// assessResult — исход одной попытки оценки: текст ответа модели (для repair),
// разобранная оценка, расход токенов и ошибка. repairable отмечает ответ, который
// был получен, но не прошёл разбор или Validate: только его имеет смысл повторять.
type assessResult struct {
	content    string
	assessment llm.Assessment
	usage      llm.Usage
	repairable bool
	err        error
}

// assessOnce выполняет ровно один запрос оценки и проверяет инварианты через
// llm.Assessment.Validate; доменный Validate здесь не ослабляется.
func (c *Client) assessOnce(ctx context.Context, messages []chatMessage) assessResult {
	body := chatRequest{
		Model:          c.model,
		Messages:       messages,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	var response chatResponse
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/chat/completions"), body, &response); err != nil {
		return assessResult{err: err}
	}
	if len(response.Choices) == 0 {
		return assessResult{err: fmt.Errorf("openai: пустой ответ модели (нет choices)")}
	}

	content := response.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return assessResult{err: fmt.Errorf("openai: пустой ответ модели")}
	}

	payload := extractJSONObject(content)

	var raw assessmentJSON
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return assessResult{content: content, usage: usageFrom(response.Usage), repairable: true,
			err: fmt.Errorf("openai: разбор оценки модели: %w", err)}
	}

	assessment := raw.toAssessment()
	if err := assessment.Validate(); err != nil {
		return assessResult{content: content, usage: usageFrom(response.Usage), repairable: true,
			err: fmt.Errorf("openai: %w", err)}
	}
	return assessResult{content: content, assessment: assessment, usage: usageFrom(response.Usage)}
}

// assessRepairMessage собирает требование к модели дозаполнить невалидный ответ:
// перечисляет недостающие/некорректные поля из текста ошибки Validate и повторяет
// обязательные для вердикта требования.
func assessRepairMessage(err error) string {
	return fmt.Sprintf("Предыдущий ответ не прошёл проверку: %s. Верни ТОЛЬКО JSON-объект с полями \"verdict\", \"signal_kind\" и \"evidence\" и дозаполни обязательные поля: для \"fits\" — signal_kind \"direct\" или \"indirect\" и хотя бы одно свидетельство с непустыми criterion, fact, source_url и reasoning; для \"hypothesis\" — signal_kind \"profile\" и свидетельство с непустым missing; для \"unsuitable\" — свидетельство с непустым mismatch.", err)
}

// ConverseStream выполняет ход диалога с потоковой отдачей реплики. Модель
// обязуется вернуть сначала текст реплики, затем строку-разделитель
// converseStreamPlanSentinel и после неё JSON-объект {targets, limits, ready}.
// Адаптер отдаёт текст по мере генерации, а хвост-структуру буферизует и
// разбирает в завершающий кадр. Так оператор видит реплику до её завершения, а
// структура плана приходит одной валидной частью.
func (c *Client) ConverseStream(ctx context.Context, r llm.ConverseRequest) iter.Seq2[llm.ConverseDelta, error] {
	return func(yield func(llm.ConverseDelta, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		if err := r.Validate(); err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}

		messages := make([]chatMessage, 0, len(r.History)+1)
		messages = append(messages, chatMessage{Role: "system", Content: converseStreamSystemPrompt})
		for _, m := range r.History {
			messages = append(messages, chatMessage{Role: m.Role, Content: m.Content})
		}
		body := chatRequest{
			Model:         c.model,
			Messages:      messages,
			Stream:        true,
			StreamOptions: &streamOptions{IncludeUsage: true},
		}

		resp, err := c.doStream(ctx, body)
		if err != nil {
			yield(llm.ConverseDelta{}, err)
			return
		}
		defer resp.Body.Close()

		var (
			content    string
			emitted    int
			planAt     = -1 // индекс начала хвоста плана; -1 — разделитель не встречен
			usage      llm.Usage
			scanner    = bufio.NewScanner(resp.Body)
			scanBuffer = make([]byte, 0, 64*1024)
		)
		// Ответы модели бывают длиннее стандартного буфера сканера; увеличиваем его,
		// чтобы длинная реплика неожиданно не обрывалась.
		scanner.Buffer(scanBuffer, 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			payload, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			payload = strings.TrimSpace(payload)
			if payload == "[DONE]" {
				break
			}

			var chunk chatChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				yield(llm.ConverseDelta{}, fmt.Errorf("openai: разбор кадра потока: %w", err))
				return
			}
			if chunk.Usage != nil {
				usage = usageFrom(chunk.Usage)
			}
			for _, choice := range chunk.Choices {
				content += choice.Delta.Content
			}

			// Пока разделитель не встречен, отдаём безопасную часть текста: не выдаём
			// префикс разделителя, если он пока не полностью пришёл.
			if planAt < 0 {
				if idx := strings.Index(content, converseStreamPlanSentinel); idx >= 0 {
					planAt = idx
					if idx > emitted {
						// Отдаём остаток текста до разделителя, снимая перенос строки,
						// которым текст отделён от разделителя. idx указывает на начало
						// ASCII-разделителя, поэтому граница среза не рвёт руну.
						if seg := strings.TrimRight(content[emitted:idx], "\n"); seg != "" {
							if !yield(llm.ConverseDelta{Text: seg}, nil) {
								return
							}
						}
						emitted = idx
					}
					continue
				}
				// Удерживаем последние байты размером с разделитель плюс отделяющий
				// перенос: так префикс разделителя и перенос не уйдут наружу раньше
				// времени.
				safe := len(content) - len(converseStreamPlanSentinel) - 1
				// Верхняя граница должна лежать на начале руны: иначе срез попадёт
				// внутрь многобайтового символа (кириллица — два байта) и фрагмент
				// перестанет быть валидным UTF-8.
				for safe > emitted && !utf8.RuneStart(content[safe]) {
					safe--
				}
				if safe > emitted {
					if !yield(llm.ConverseDelta{Text: content[emitted:safe]}, nil) {
						return
					}
					emitted = safe
				}
			}
		}
		if err := scanner.Err(); err != nil {
			yield(llm.ConverseDelta{}, fmt.Errorf("openai: чтение потока: %w", err))
			return
		}

		// Разделитель так и не встретился: принудительно выдаём удержанный хвост,
		// чтобы склейка фрагментов совпала с полной репликой и ничего не потерялось.
		if planAt < 0 && emitted < len(content) {
			if !yield(llm.ConverseDelta{Text: content[emitted:]}, nil) {
				return
			}
		}

		text := content
		tail := ""
		if planAt >= 0 {
			text = strings.TrimRight(content[:planAt], "\n")
			tail = content[planAt+len(converseStreamPlanSentinel):]
		}

		reply := llm.ConverseReply{Text: text}
		if strings.TrimSpace(tail) != "" {
			var raw converseJSON
			if err := json.Unmarshal([]byte(extractJSONObject(tail)), &raw); err != nil {
				yield(llm.ConverseDelta{}, fmt.Errorf("openai: разбор плана потока: %w", err))
				return
			}
			reply.Ready = raw.Ready
			reply.Limits = raw.Limits.toPlanLimits()
			for _, t := range raw.Targets {
				target := llm.Target{Query: t.Query, URL: t.URL}
				if err := target.Validate(); err != nil {
					continue
				}
				reply.Targets = append(reply.Targets, target)
			}
		}
		if err := reply.Validate(); err != nil {
			yield(llm.ConverseDelta{}, fmt.Errorf("openai: %w", err))
			return
		}
		yield(llm.ConverseDelta{Reply: &reply, Usage: usage}, nil)
	}
}

// doStream отправляет потоковый chat completion и возвращает ответ для чтения
// кадров. Неуспешный статус читается целиком и возвращается обычной ошибкой Go.
func (c *Client) doStream(ctx context.Context, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: сериализация запроса: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/chat/completions"), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai: создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: запрос потока: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai: запрос потока: статус %d: %s", resp.StatusCode, truncate(string(data)))
	}
	return resp, nil
}

// doJSON отправляет JSON-запрос и декодирует ответ. Неуспешный статус и ошибка
// транспорта возвращаются обычной ошибкой Go без утечки типов http.Response.
func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("openai: сериализация запроса: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("openai: создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("openai: чтение ответа: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("openai: %s %s: статус %d: %s", method, url, resp.StatusCode, truncate(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("openai: декодирование ответа: %w", err)
	}
	return nil
}

// endpoint склеивает базовый адрес и путь ресурса.
func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

// extractJSONArray вырезает из ответа модели фрагмент от первой '[' до последней
// ']'. Если модель вернула «голый» массив, он и вернётся без изменений.
func extractJSONArray(content string) string {
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start == -1 || end == -1 || end < start {
		return content
	}
	return content[start : end+1]
}

// extractJSONObject вырезает из ответа модели фрагмент от первой '{' до последней
// '}'. Если модель вернула «голый» объект, он и вернётся без изменений.
func extractJSONObject(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || end < start {
		return content
	}
	return content[start : end+1]
}

// truncate ограничивает тело ответа для читаемой ошибки.
func truncate(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// planSystemPrompt — системная инструкция: модель обязана разложить запрос на
// JSON-объект {"targets": [...]} с целями, где задан ровно один из способов
// поиска, и учесть исключения интерпретации (информационные порталы, каталоги,
// магазины, отдельные статьи).
const planSystemPrompt = `Ты планируешь поиск потенциальных клиентов. Разложи запрос пользователя на серию поисковых запросов и стартовых URL, ведущих к сайтам компаний — потенциальных клиентов. Верни ТОЛЬКО JSON-объект с полем "targets" — массивом целей. Каждая цель — объект ровно с одним из двух полей: либо "query" (текст для поисковой системы), либо "url" (стартовый адрес для обхода), но не оба и не пусто. Если в сообщении пользователя приведены критерии релевантности (целевой лид, признаки потребности, исключения), учитывай их: веди план к целевым компаниям и признакам потребности и не предлагай цели к исключённым источникам — информационным порталам, каталогам, магазинам и отдельным статьям.`

// suggestSystemPrompt — системная инструкция: модель обязана по описанию задачи
// вернуть JSON-массив предложений {sector, niche, seed_queries}.
const suggestSystemPrompt = `Ты подбираешь цели для поиска потенциальных клиентов. По описанию бизнеса или задачи пользователя предложи сектора, ниши и готовые поисковые запросы (seed-запросы). Верни ТОЛЬКО JSON-массив объектов с полями "sector" (сектор), "niche" (ниша) и "seed_queries" (массив готовых поисковых запросов). В каждом объекте сектор и ниша непустые, а seed_queries содержит хотя бы один непустой запрос.`

// converseSystemPrompt — системная инструкция диалога: модель обязана задавать
// уточняющие вопросы, пока данных мало, и вернуть JSON-объект
// {text, targets, ready}.
const converseSystemPrompt = `Ты помогаешь пользователю договориться о стратегии поиска потенциальных клиентов. Пока данных мало, задавай один короткий уточняющий вопрос. Когда данных достаточно, опиши план поиска и отметь его готовым. Верни ТОЛЬКО JSON-объект с полями "text" (твоя реплика пользователю), "targets" (массив целей поиска, каждая — объект ровно с одним из полей "query" или "url") и "ready" (true, когда план готов). В поле "text" всегда непустая реплика.`

// converseStreamPlanSentinel — разделитель между потоковым текстом реплики и
// JSON-хвостом плана в потоковом ответе модели.
const converseStreamPlanSentinel = "===PLAN==="

// converseStreamSystemPrompt — системная инструкция потокового диалога: модель
// обязана сначала выдать текст реплики обычным текстом (его оператор видит по мере
// генерации), затем строку-разделитель и после неё — JSON-объект плана. Так
// реплика читается постепенно, а структура плана приходит одной валидной частью.
const converseStreamSystemPrompt = `Ты помогаешь пользователю договориться о стратегии поиска потенциальных клиентов. Сначала напиши свою реплику пользователю обычным текстом: пока данных мало — одно короткое уточняющее вопрос, когда данных достаточно — описание плана поиска. Затем с новой строки напиши разделитель ===PLAN=== и после него ТОЛЬКО JSON-объект с полями "targets" (массив целей поиска, каждая — объект ровно с одним из полей "query" или "url"), "limits" (объект с полями "max_depth" — глубина обхода и "max_time_seconds" — время работы в секундах; поля необязательны) и "ready" (true, когда план готов). Текст реплики до разделителя — обычными словами, без JSON.`

// interpretSystemPrompt — системная инструкция интерпретации: модель обязана
// превратить бизнес-запрос в критерии релевантности и вернуть JSON-объект
// {target_lead, need_signals, exclusions, hypothesis_signals, uncertainty}.
const interpretSystemPrompt = `Ты интерпретируешь бизнес-запрос оператора в критерии релевантности потенциальных клиентов. Определи, кто является целевым лидом, какие признаки искомой потребности искать, что считается исключением и какие признаки дают лишь гипотезу. Верни ТОЛЬКО JSON-объект с полями "target_lead" (кто целевой лид), "need_signals" (массив строк: признаки искомой потребности), "exclusions" (массив строк: что считается исключением), "hypothesis_signals" (массив строк: признаки, дающие лишь гипотезу) и "uncertainty" (true, если данных для определения признаков потребности недостаточно; тогда не выдумывай критерии).`

// assessSystemPrompt — системная инструкция оценки: модель обязана вернуть
// JSON-объект {verdict, signal_kind, evidence} с свидетельствами.
const assessSystemPrompt = `Ты оцениваешь компанию относительно критериев релевантности задания поиска потенциальных клиентов. По описанию компании и собранному материалу определи вердикт и свидетельства. Верни ТОЛЬКО JSON-объект с полями "verdict", "signal_kind" и "evidence". Поле "verdict" принимает одно из значений: "fits" (подходит), "hypothesis" (гипотеза), "unsuitable" (не подходит). Поле "signal_kind" принимает одно из значений: "direct" (прямое заявление), "indirect" (косвенный сигнал), "profile" (совпадение по профилю). Поле "evidence" — массив объектов с полями "criterion" (критерий из запроса), "fact" (наблюдаемый факт), "source_url" (ссылка на источник, подтверждающий факт), "reasoning" (объяснение связи факта с потребностью), "signal_kind", "missing" (какого свидетельства не хватает, для гипотезы) и "mismatch" (установленное несоответствие, для вердикта unsuitable). Для вердикта fits верни signal_kind direct или indirect и хотя бы одно свидетельство с непустыми criterion, fact, source_url и reasoning. Для вердикта hypothesis верни signal_kind profile и хотя бы одно свидетельство с непустым missing. Для вердикта unsuitable верни хотя бы одно свидетельство с непустым mismatch.`

// chatRequest — тело OpenAI-совместимого chat completion запроса.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	StreamOptions  *streamOptions  `json:"stream_options,omitempty"`
}

// streamOptions — опции потокового запроса. IncludeUsage просит провайдера
// прислать учёт токенов завершающим кадром.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatChunk — один кадр потокового ответа: фрагмент текста ассистента и
// (в завершающем кадре) учёт токенов.
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
}

// chatMessage — одно сообщение диалога.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// responseFormat — запрос JSON-ответа от модели.
type responseFormat struct {
	Type string `json:"type"`
}

// chatResponse — ответ chat completion. Поля — подмножество ответа провайдера,
// достаточное для извлечения текста ассистента и учёта токенов.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
}

// usageJSON — учёт токенов в ответе chat completion. Поля необязательны: провайдер
// может не вернуть usage.
type usageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// usageFrom отображает usage-ответ провайдера в llm.Usage; nil даёт нулевой расход.
func usageFrom(u *usageJSON) llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// targetJSON — пункт плана в JSON-ответе модели. Явные теги фиксируют имена
// полей, которые модель обязана вернуть; llm.Target намеренно не несёт тегов,
// потому что его форма — забота контракта, а не сериализации провайдера.
type targetJSON struct {
	Query string `json:"query"`
	URL   string `json:"url"`
}

// planJSON — план обхода в JSON-ответе модели: объект с массивом целей. Явный
// тег фиксирует имя поля, которое модель обязана вернуть; основной формат ответа.
type planJSON struct {
	Targets []targetJSON `json:"targets"`
}

// suggestionJSON — предложение в JSON-ответе модели. Явные теги фиксируют имена
// полей, которые модель обязана вернуть; llm.Suggestion намеренно не несёт тегов,
// потому что его форма — забота контракта, а не сериализации провайдера.
type suggestionJSON struct {
	Sector      string   `json:"sector"`
	Niche       string   `json:"niche"`
	SeedQueries []string `json:"seed_queries"`
}

// converseJSON — ответ модели на ход диалога. Явные теги фиксируют имена полей,
// которые модель обязана вернуть; llm.ConverseReply намеренно не несёт тегов,
// потому что его форма — забота контракта, а не сериализации провайдера.
type converseJSON struct {
	Text    string       `json:"text"`
	Targets []targetJSON `json:"targets"`
	Limits  limitJSON    `json:"limits"`
	Ready   bool         `json:"ready"`
}

// limitJSON — операторские лимиты обхода в JSON-ответе модели. Поля необязательны:
// модель возвращает только те лимиты, которые согласовала в диалоге.
type limitJSON struct {
	MaxDepth       int `json:"max_depth"`
	MaxTimeSeconds int `json:"max_time_seconds"`
}

// toPlanLimits отображает лимиты JSON-ответа в лимиты контракта.
func (l limitJSON) toPlanLimits() llm.PlanLimits {
	return llm.PlanLimits{
		MaxDepth:       l.MaxDepth,
		MaxTimeSeconds: l.MaxTimeSeconds,
	}
}

// interpretationJSON — критерии релевантности в JSON-ответе модели. Явные теги
// фиксируют имена полей, которые модель обязана вернуть; llm.Interpretation
// намеренно не несёт тегов, потому что его форма — забота контракта, а не
// сериализации провайдера.
type interpretationJSON struct {
	TargetLead        string   `json:"target_lead"`
	NeedSignals       []string `json:"need_signals"`
	Exclusions        []string `json:"exclusions"`
	HypothesisSignals []string `json:"hypothesis_signals"`
	Uncertainty       bool     `json:"uncertainty"`
}

// toInterpretation отображает JSON-ответ модели в критерии контракта, отбрасывая
// пустые строки списков.
func (i interpretationJSON) toInterpretation() llm.Interpretation {
	return llm.Interpretation{
		TargetLead:        strings.TrimSpace(i.TargetLead),
		NeedSignals:       cleanStrings(i.NeedSignals),
		Exclusions:        cleanStrings(i.Exclusions),
		HypothesisSignals: cleanStrings(i.HypothesisSignals),
		Uncertainty:       i.Uncertainty,
	}
}

// assessmentJSON — оценка компании в JSON-ответе модели.
type assessmentJSON struct {
	Verdict    string                   `json:"verdict"`
	SignalKind string                   `json:"signal_kind"`
	Evidence   []assessmentEvidenceJSON `json:"evidence"`
}

// assessmentEvidenceJSON — свидетельство оценки в JSON-ответе модели.
type assessmentEvidenceJSON struct {
	Criterion  string `json:"criterion"`
	Fact       string `json:"fact"`
	SourceURL  string `json:"source_url"`
	Reasoning  string `json:"reasoning"`
	SignalKind string `json:"signal_kind"`
	Missing    string `json:"missing"`
	Mismatch   string `json:"mismatch"`
}

// toAssessment отображает JSON-ответ модели в оценку контракта.
func (a assessmentJSON) toAssessment() llm.Assessment {
	assessment := llm.Assessment{
		Verdict:    strings.TrimSpace(a.Verdict),
		SignalKind: strings.TrimSpace(a.SignalKind),
	}
	for _, e := range a.Evidence {
		assessment.Evidence = append(assessment.Evidence, llm.AssessmentEvidence{
			Criterion:  strings.TrimSpace(e.Criterion),
			Fact:       strings.TrimSpace(e.Fact),
			SourceURL:  strings.TrimSpace(e.SourceURL),
			Reasoning:  strings.TrimSpace(e.Reasoning),
			SignalKind: strings.TrimSpace(e.SignalKind),
			Missing:    strings.TrimSpace(e.Missing),
			Mismatch:   strings.TrimSpace(e.Mismatch),
		})
	}
	return assessment
}

// planUserMessage собирает пользовательское сообщение для планирования: запрос и
// (если они заданы) критерии релевантности. Признаки потребности и исключения
// направляют план к сайтам компаний, а при явной неопределённости модель
// опирается только на текст запроса.
func planUserMessage(r llm.PlanRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Запрос: %s\n", r.Query)
	if r.Interpretation.Uncertainty {
		b.WriteString("Критерии релевантности не определены: данных недостаточно. Опирайся только на текст запроса и не выдумывай критерии.\n")
		return b.String()
	}
	if strings.TrimSpace(r.Interpretation.TargetLead) != "" {
		fmt.Fprintf(&b, "Целевой лид: %s\n", r.Interpretation.TargetLead)
	}
	if len(r.Interpretation.NeedSignals) > 0 {
		fmt.Fprintf(&b, "Признаки потребности: %s\n", strings.Join(r.Interpretation.NeedSignals, "; "))
	}
	if len(r.Interpretation.Exclusions) > 0 {
		fmt.Fprintf(&b, "Исключения: %s\n", strings.Join(r.Interpretation.Exclusions, "; "))
	}
	if len(r.Interpretation.HypothesisSignals) > 0 {
		fmt.Fprintf(&b, "Признаки гипотезы: %s\n", strings.Join(r.Interpretation.HypothesisSignals, "; "))
	}
	return b.String()
}

// assessUserMessage собирает пользовательское сообщение для оценки: компанию,
// критерии релевантности и собранный материал.
func assessUserMessage(r llm.AssessRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Задание: %s\n", r.JobID)
	if strings.TrimSpace(r.CompanyName) != "" {
		fmt.Fprintf(&b, "Компания: %s\n", r.CompanyName)
	}
	if strings.TrimSpace(r.CompanyDescription) != "" {
		fmt.Fprintf(&b, "Описание компании: %s\n", r.CompanyDescription)
	}
	if strings.TrimSpace(r.Interpretation.TargetLead) != "" {
		fmt.Fprintf(&b, "Целевой лид: %s\n", r.Interpretation.TargetLead)
	}
	if len(r.Interpretation.NeedSignals) > 0 {
		fmt.Fprintf(&b, "Признаки потребности: %s\n", strings.Join(r.Interpretation.NeedSignals, "; "))
	}
	if len(r.Interpretation.Exclusions) > 0 {
		fmt.Fprintf(&b, "Исключения: %s\n", strings.Join(r.Interpretation.Exclusions, "; "))
	}
	if len(r.Interpretation.HypothesisSignals) > 0 {
		fmt.Fprintf(&b, "Признаки гипотезы: %s\n", strings.Join(r.Interpretation.HypothesisSignals, "; "))
	}
	if r.Interpretation.Uncertainty {
		b.WriteString("Критерии определены неполно: данных недостаточно.\n")
	}
	if len(r.Material) > 0 {
		b.WriteString("Материал о компании:\n")
		for _, m := range r.Material {
			fmt.Fprintf(&b, "- источник %s: %s\n", m.URL, m.Text)
		}
	}
	return b.String()
}

// cleanStrings обрезает пробелы в строках и отбрасывает пустые.
func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}
