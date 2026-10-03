package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/llm"
)

// ErrInvalidEmailRequest — запрос генерации письма некорректен: пустой оффер,
// отсутствующий или несуществующий лид. Владелец не выдумывает письмо по
// несформулированному запросу.
var ErrInvalidEmailRequest = errors.New("app: запрос генерации письма некорректен")

// ErrEmailGenerationUnavailable — порт LLM не сконфигурирован: писать письмо
// нечем. Письмо в этом случае не выдумывается.
var ErrEmailGenerationUnavailable = errors.New("app: генерация письма недоступна: LLM не сконфигурирован")

// DraftEmailInput — вход сценария генерации письма: оффер клиента и идентификатор
// найденного лида.
type DraftEmailInput struct {
	// RequestID — заявка, под которую генерируется письмо (сквозная ссылка).
	RequestID string
	// TenantID — тенант-владелец пула.
	TenantID string
	// LeadID — идентификатор найденного лида (нормализованный домен компании).
	LeadID string
	// Criteria — оффер: текст предложения и цель письма.
	Criteria string
	// Language — язык письма; пусто — русский.
	Language string
	// Tone — желаемый тон письма; пусто — значение по умолчанию промпта.
	Tone string
}

// EmailDraft — сгенерированное письмо прикладного слоя: тема, тело и язык.
type EmailDraft struct {
	Subject  string
	Body     string
	Language string
}

// DraftEmailForLead — use case генерации персонализированного письма под лида и
// оффер. Читает карточку и контакт лида, строит промпт из оффера и данных лида и
// вызывает порт LLM (один ход диалога как primitive свободного текста). Без
// подключённого порта LLM честно сообщает о недоступности, а не выдумывает письмо.
type DraftEmailForLead struct {
	// Profiles / Contacts читают данные лида для персонализации. Обязательны:
	// без них письмо не к чему привязать.
	Profiles ports.LeadProfileRepository
	Contacts ports.ContactRepository
	// LLM — существующий порт LLM; nil — генерация недоступна.
	LLM llm.Port
}

// Run генерирует письмо по офферу и найденному лиду. Пустой оффер или лид дают
// ErrInvalidEmailRequest; отсутствующий порт LLM или сбой вызова — ошибку,
// оборачивающую ErrEmailGenerationUnavailable.
func (u *DraftEmailForLead) Run(ctx context.Context, in DraftEmailInput) (EmailDraft, error) {
	criteria := strings.TrimSpace(in.Criteria)
	leadID := strings.TrimSpace(in.LeadID)
	if criteria == "" || leadID == "" {
		return EmailDraft{}, fmt.Errorf("%w: оффер и идентификатор лида обязательны", ErrInvalidEmailRequest)
	}
	if u.LLM == nil {
		return EmailDraft{}, ErrEmailGenerationUnavailable
	}
	if u.Profiles == nil {
		return EmailDraft{}, fmt.Errorf("%w: репозиторий карточек не подключён", ErrEmailGenerationUnavailable)
	}

	company, contact, err := u.leadContext(ctx, leadID)
	if err != nil {
		return EmailDraft{}, err
	}

	prompt := buildEmailPrompt(in, company, contact)
	reply, _, err := u.LLM.Converse(ctx, llm.ConverseRequest{
		History: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return EmailDraft{}, fmt.Errorf("%w: вызов LLM: %v", ErrEmailGenerationUnavailable, err)
	}

	subject, body := parseGeneratedEmail(reply.Text)
	if strings.TrimSpace(body) == "" {
		return EmailDraft{}, fmt.Errorf("%w: LLM не вернул тело письма", ErrEmailGenerationUnavailable)
	}
	return EmailDraft{Subject: subject, Body: body, Language: strings.TrimSpace(in.Language)}, nil
}

// leadContext загружает карточку лида и первый контакт с email (для роли).
// Отсутствие карточки — доменный отказ: персонализировать несуществующий лид
// нельзя. Отсутствие контакта не мешает генерации.
func (u *DraftEmailForLead) leadContext(ctx context.Context, leadID string) (*domain.Company, *domain.Contact, error) {
	company, err := u.Profiles.FindByID(ctx, domain.CompanyID(leadID))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return nil, nil, fmt.Errorf("%w: лид %s не найден", ErrInvalidEmailRequest, leadID)
	}
	if err != nil {
		return nil, nil, err
	}
	if u.Contacts == nil {
		return company, nil, nil
	}
	contacts, err := u.Contacts.ListByCompany(ctx, company.ID())
	if err != nil {
		return company, nil, nil
	}
	for _, contact := range contacts {
		if email := contact.Email(); email != nil && strings.TrimSpace(email.Value) != "" {
			return company, contact, nil
		}
	}
	return company, nil, nil
}

// buildEmailPrompt собирает промпт из оффера и данных лида. В промпт идут только
// обезличенные данные лида: название, домен, описание и роль контакта; адрес и
// телефон в промпт не попадают (payload-гигиена, data-governance).
func buildEmailPrompt(in DraftEmailInput, company *domain.Company, contact *domain.Contact) string {
	var b strings.Builder
	b.WriteString("Ты — опытный B2B-копирайтер. Напиши персонализированное исходящее письмо потенциальному клиенту.\n\n")
	b.WriteString("Оффер клиента (что предлагаем и зачем):\n")
	b.WriteString(strings.TrimSpace(in.Criteria))
	b.WriteString("\n\nДанные найденного лида:\n")
	if company != nil {
		if name := strings.TrimSpace(company.Name().Value); name != "" {
			b.WriteString("Компания: " + name + "\n")
		}
		b.WriteString("Домен: " + company.ID().String() + "\n")
		if desc := strings.TrimSpace(company.Description().Value); desc != "" {
			b.WriteString("Описание: " + desc + "\n")
		}
	}
	if contact != nil {
		if role := contact.Role(); role != nil && strings.TrimSpace(role.Value) != "" {
			b.WriteString("Роль контакта: " + role.Value + "\n")
		}
	}

	language := strings.TrimSpace(in.Language)
	if language == "" {
		language = "русский"
	}
	b.WriteString("\nТребования: язык письма — " + language)
	if tone := strings.TrimSpace(in.Tone); tone != "" {
		b.WriteString(", тон — " + tone)
	}
	b.WriteString(".\n\n")
	b.WriteString("В поле \"text\" верни ТОЛЬКО письмо: первая строка — тема письма, затем пустая строка, затем тело письма. " +
		"Без пояснений, служебных пометок и JSON внутри текста.")
	return b.String()
}

// parseGeneratedEmail разбирает ответ модели в тему и тело. Принимаются две формы:
// JSON-объект {"subject","body"} и разделённый переводом строки текст, где первая
// строка — тема. Неразобранная форма честно отдаёт весь текст телом без выдумки
// темы.
func parseGeneratedEmail(text string) (subject, body string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	var payload struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(extractJSONObject(text)), &payload); err == nil && strings.TrimSpace(payload.Body) != "" {
		return strings.TrimSpace(payload.Subject), strings.TrimSpace(payload.Body)
	}

	lines := strings.SplitN(text, "\n", 2)
	subject = trimSubjectPrefix(lines[0])
	if len(lines) == 2 {
		body = strings.TrimSpace(lines[1])
		return subject, body
	}
	return "", text
}

// extractJSONObject выделяет первый JSON-объект из ответа модели: адаптер LLM
// может вернуть обрамление вокруг объекта.
func extractJSONObject(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || end < start {
		return content
	}
	return content[start : end+1]
}

// trimSubjectPrefix снимает необязательный префикс темы («Тема:», «Subject:»),
// если модель его добавила.
func trimSubjectPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"Тема:", "тема:", "Subject:", "subject:"} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return line
}
