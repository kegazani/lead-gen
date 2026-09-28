package domain

import (
	"fmt"
	"strings"
	"time"
)

// TemplateID — идентификатор шаблона письма. В срезе генерируется прикладным
// слоем (стабильный ключ); домен лишь требует непустое значение.
type TemplateID string

// validate проверяет непустоту идентификатора.
func (id TemplateID) validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return ErrEmptyTemplateID
	}
	return nil
}

// String возвращает идентификатор строкой.
func (id TemplateID) String() string { return string(id) }

// MergeVariableKey — ключ переменной из каталога переменных. Канонический набор
// ключей принадлежит домену (capability email-template-library): именно он
// определяет инвариант «переменная из каталога». Значения и подписи переменных
// живут в прикладном слое.
type MergeVariableKey string

// Согласованный набор ключей каталога переменных. Начальный набор привязан к
// доступным полям карточки компании и контакта; расширяется только добавлением
// ключей.
const (
	// MergeVariableCompany — название компании.
	MergeVariableCompany MergeVariableKey = "company"
	// MergeVariableCompanyDomain — домен компании (нормализованный идентификатор).
	MergeVariableCompanyDomain MergeVariableKey = "company_domain"
	// MergeVariableCompanyDescription — описание компании.
	MergeVariableCompanyDescription MergeVariableKey = "company_description"
	// MergeVariableContact — роль контакта.
	MergeVariableContact MergeVariableKey = "contact"
	// MergeVariableContactEmail — email контакта.
	MergeVariableContactEmail MergeVariableKey = "contact_email"
	// MergeVariableContactPhone — телефон контакта.
	MergeVariableContactPhone MergeVariableKey = "contact_phone"
	// MergeVariableIndustry — отрасль компании (источника данных пока нет, значение
	// всегда пусто и уходит в объявленный fallback).
	MergeVariableIndustry MergeVariableKey = "industry"
)

// mergeVariableCatalog — канонический порядок ключей каталога.
var mergeVariableCatalog = []MergeVariableKey{
	MergeVariableCompany,
	MergeVariableCompanyDomain,
	MergeVariableCompanyDescription,
	MergeVariableContact,
	MergeVariableContactEmail,
	MergeVariableContactPhone,
	MergeVariableIndustry,
}

// MergeVariableKeys возвращает канонический набор ключей каталога в порядке
// объявления. Возвращается копия: вызывающий не может изменить каталог.
func MergeVariableKeys() []MergeVariableKey {
	return append([]MergeVariableKey(nil), mergeVariableCatalog...)
}

// IsMergeVariable сообщает, входит ли ключ в каталог переменных.
func IsMergeVariable(key MergeVariableKey) bool { return key.valid() }

// valid сообщает, известен ли ключ каталогу.
func (k MergeVariableKey) valid() bool {
	for _, known := range mergeVariableCatalog {
		if k == known {
			return true
		}
	}
	return false
}

// MergeVariable — value object объявленной переменной шаблона: ключ каталога и
// непустое fallback-значение. fallback обязателен, чтобы незаполненное поле не
// давало пустую строку.
type MergeVariable struct {
	// Key — ключ каталога переменных.
	Key MergeVariableKey
	// Fallback — значение подстановки, если поле лида пусто.
	Fallback string
}

// NewMergeVariable создаёт объявленную переменную. Ключ обязан входить в каталог,
// fallback — быть непустым после обрезки пробелов.
func NewMergeVariable(key, fallback string) (MergeVariable, error) {
	parsed := MergeVariableKey(strings.TrimSpace(key))
	if !parsed.valid() {
		return MergeVariable{}, fmt.Errorf("%w: %q", ErrUnknownMergeVariable, key)
	}
	trimmed := strings.TrimSpace(fallback)
	if trimmed == "" {
		return MergeVariable{}, fmt.Errorf("%w: %s", ErrEmptyMergeFallback, parsed)
	}
	return MergeVariable{Key: parsed, Fallback: trimmed}, nil
}

// validate проверяет согласованность объявленной переменной.
func (v MergeVariable) validate() error {
	if !v.Key.valid() {
		return fmt.Errorf("%w: %q", ErrUnknownMergeVariable, v.Key)
	}
	if strings.TrimSpace(v.Fallback) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyMergeFallback, v.Key)
	}
	return nil
}

// TemplateReference — value object ссылки на редакцию шаблона вида
// `template_id@revision`. Ссылка неизменяема и фиксируется в истории отправки.
type TemplateReference struct {
	// ID — идентификатор шаблона.
	ID TemplateID
	// Revision — номер редакции, использованной при отправке.
	Revision int
}

// NewTemplateReference собирает ссылку из идентификатора и ревизии. Ревизия
// обязана быть положительной.
func NewTemplateReference(id TemplateID, revision int) (TemplateReference, error) {
	if err := id.validate(); err != nil {
		return TemplateReference{}, err
	}
	if revision < 1 {
		return TemplateReference{}, fmt.Errorf("%w: %s@%d", ErrInvalidTemplateReference, id, revision)
	}
	return TemplateReference{ID: id, Revision: revision}, nil
}

// String возвращает ссылку в каноническом виде `template_id@revision`.
func (r TemplateReference) String() string {
	return fmt.Sprintf("%s@%d", r.ID, r.Revision)
}

// ParseTemplateReference разбирает ссылку вида `template_id@revision`. Формат
// закрыт: ровно один разделитель `@`, непустой идентификатор, положительная
// ревизия.
func ParseTemplateReference(raw string) (TemplateReference, error) {
	value := strings.TrimSpace(raw)
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return TemplateReference{}, fmt.Errorf("%w: %q", ErrInvalidTemplateReference, raw)
	}
	reference, err := NewTemplateReference(TemplateID(value[:at]), atoiSafe(value[at+1:]))
	if err != nil {
		return TemplateReference{}, fmt.Errorf("%w: %q", ErrInvalidTemplateReference, raw)
	}
	return reference, nil
}

// atoiSafe разбирает положительное целое без пробелов.
func atoiSafe(raw string) int {
	value := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		value = value*10 + int(r-'0')
	}
	return value
}

// Placeholders возвращает плейсхолдеры `{{ … }}` в порядке появления, включая
// повторы. Разбор наивный и не поддерживает вложенные фигурные скобки (то же
// ограничение унаследовано от прежней подстановки merge-полей): плейсхолдер —
// это пара `{{` и `}}`, между ними — имя переменной без пробелов по краям.
// Незакрытый плейсхолдер и текст без плейсхолдеров дают пустой результат.
func Placeholders(text string) []string {
	var found []string
	rest := text
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			return found
		}
		end := strings.Index(rest[start+2:], "}}")
		if end < 0 {
			return found
		}
		end += start + 2
		found = append(found, strings.TrimSpace(rest[start+2:end]))
		rest = rest[end+2:]
	}
}

// EmailTemplate — aggregate root email-template (владелец lead-management):
// переиспользуемое письмо с именем, темой, телом и явно объявленным набором
// переменных. Правка выполняется «на месте» и повышает `revision`; история
// версий текста не хранится — неизменяемость прошлых отправок защищает
// outbound-message снимком собранного тела.
type EmailTemplate struct {
	id        TemplateID
	name      string
	subject   string
	body      string
	variables []MergeVariable
	revision  int
	createdAt time.Time
	updatedAt time.Time
}

// CreateTemplateParams — параметры создания шаблона.
type CreateTemplateParams struct {
	// ID — идентификатор шаблона (задаёт прикладной слой).
	ID string
	// Name — непустое имя шаблона.
	Name string
	// Subject — непустая тема письма.
	Subject string
	// Body — непустое тело письма.
	Body string
	// Variables — объявленные переменные (подмножество каталога).
	Variables []MergeVariable
	// CreatedAt — момент создания.
	CreatedAt time.Time
}

// EditTemplateParams — параметры правки шаблона. Идентификатор не меняется.
type EditTemplateParams struct {
	// Name — непустое имя шаблона.
	Name string
	// Subject — непустая тема письма.
	Subject string
	// Body — непустое тело письма.
	Body string
	// Variables — новое объявленное подмножество каталога.
	Variables []MergeVariable
	// UpdatedAt — момент правки.
	UpdatedAt time.Time
}

// CreateTemplate создаёт шаблон и проверяет инварианты: имя, тема и тело непусты
// после обрезки пробелов; объявленные переменные — подмножество каталога, без
// дублей и с непустым fallback; каждый плейсхолдер темы и тела объявлен. Ревизия
// нового шаблона равна единице.
func CreateTemplate(params CreateTemplateParams) (*EmailTemplate, error) {
	id := TemplateID(strings.TrimSpace(params.ID))
	if err := id.validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.Name)
	subject := strings.TrimSpace(params.Subject)
	body := strings.TrimSpace(params.Body)
	if err := validateTemplateFields(name, subject, body); err != nil {
		return nil, err
	}
	variables, err := normalizeVariables(params.Variables)
	if err != nil {
		return nil, err
	}
	if err := validatePlaceholders(subject, body, variables); err != nil {
		return nil, err
	}
	now := params.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &EmailTemplate{
		id:        id,
		name:      name,
		subject:   subject,
		body:      body,
		variables: variables,
		revision:  1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// EditTemplate правит шаблон «на месте», проверяя те же инварианты и увеличивая
// ревизию. Идентификатор и момент создания не меняются.
func (t *EmailTemplate) EditTemplate(params EditTemplateParams) error {
	name := strings.TrimSpace(params.Name)
	subject := strings.TrimSpace(params.Subject)
	body := strings.TrimSpace(params.Body)
	if err := validateTemplateFields(name, subject, body); err != nil {
		return err
	}
	variables, err := normalizeVariables(params.Variables)
	if err != nil {
		return err
	}
	if err := validatePlaceholders(subject, body, variables); err != nil {
		return err
	}
	updatedAt := params.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	t.name = name
	t.subject = subject
	t.body = body
	t.variables = variables
	t.revision++
	t.updatedAt = updatedAt
	return nil
}

// DeleteTemplate — команда удаления на aggregate root. Снятие записи выполняет
// репозиторий, но удалять можно только целостный шаблон: команда повторно
// проверяет инварианты агрегата, поэтому нарушение не может быть удалено молча.
func (t *EmailTemplate) DeleteTemplate() error {
	if t == nil {
		return ErrEmptyTemplateID
	}
	if err := t.validateInvariants(); err != nil {
		return err
	}
	return nil
}

// validateInvariants повторно проверяет инварианты агрегата.
func (t *EmailTemplate) validateInvariants() error {
	if err := t.id.validate(); err != nil {
		return err
	}
	if err := validateTemplateFields(t.name, t.subject, t.body); err != nil {
		return err
	}
	if _, err := normalizeVariables(t.variables); err != nil {
		return err
	}
	return validatePlaceholders(t.subject, t.body, t.variables)
}

// ID возвращает идентификатор шаблона.
func (t *EmailTemplate) ID() TemplateID { return t.id }

// Name возвращает имя шаблона.
func (t *EmailTemplate) Name() string { return t.name }

// Subject возвращает тему письма.
func (t *EmailTemplate) Subject() string { return t.subject }

// Body возвращает тело письма.
func (t *EmailTemplate) Body() string { return t.body }

// Variables возвращает объявленные переменные (копию).
func (t *EmailTemplate) Variables() []MergeVariable {
	return append([]MergeVariable(nil), t.variables...)
}

// Revision возвращает номер редакции.
func (t *EmailTemplate) Revision() int { return t.revision }

// Reference возвращает ссылку на текущую редакцию шаблона.
func (t *EmailTemplate) Reference() TemplateReference {
	return TemplateReference{ID: t.id, Revision: t.revision}
}

// CreatedAt возвращает момент создания.
func (t *EmailTemplate) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt возвращает момент последней правки.
func (t *EmailTemplate) UpdatedAt() time.Time { return t.updatedAt }

// validateTemplateFields проверяет непустоту имени, темы и тела после обрезки.
func validateTemplateFields(name, subject, body string) error {
	if name == "" {
		return ErrEmptyTemplateName
	}
	if subject == "" {
		return ErrEmptyTemplateSubject
	}
	if body == "" {
		return ErrEmptyTemplateBody
	}
	return nil
}

// normalizeVariables проверяет объявленные переменные: каждая входит в каталог,
// имеет непустой fallback, ключи не повторяются. Возвращает копию списка.
func normalizeVariables(variables []MergeVariable) ([]MergeVariable, error) {
	out := make([]MergeVariable, 0, len(variables))
	seen := make(map[MergeVariableKey]bool, len(variables))
	for _, variable := range variables {
		if err := variable.validate(); err != nil {
			return nil, err
		}
		if seen[variable.Key] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateMergeVariable, variable.Key)
		}
		seen[variable.Key] = true
		out = append(out, MergeVariable{Key: variable.Key, Fallback: strings.TrimSpace(variable.Fallback)})
	}
	return out, nil
}

// validatePlaceholders проверяет, что каждый плейсхолдер темы и тела входит в
// объявленный набор переменных. Объявленная, но неиспользованная переменная
// допустима: она заранее описывает вход шаблона.
func validatePlaceholders(subject, body string, variables []MergeVariable) error {
	declared := make(map[string]bool, len(variables))
	for _, variable := range variables {
		declared[string(variable.Key)] = true
	}
	var undeclared []string
	seen := map[string]bool{}
	for _, field := range []string{subject, body} {
		for _, placeholder := range Placeholders(field) {
			if declared[placeholder] || seen[placeholder] {
				continue
			}
			seen[placeholder] = true
			undeclared = append(undeclared, placeholder)
		}
	}
	if len(undeclared) > 0 {
		return fmt.Errorf("%w: %s", ErrUndeclaredPlaceholder, strings.Join(undeclared, ", "))
	}
	return nil
}

// EmailTemplateSnapshot — сохранённое состояние aggregate root email-template.
type EmailTemplateSnapshot struct {
	ID        TemplateID
	Name      string
	Subject   string
	Body      string
	Variables []MergeVariable
	Revision  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Snapshot возвращает состояние агрегата для сохранения.
func (t *EmailTemplate) Snapshot() EmailTemplateSnapshot {
	return EmailTemplateSnapshot{
		ID:        t.id,
		Name:      t.name,
		Subject:   t.subject,
		Body:      t.body,
		Variables: append([]MergeVariable(nil), t.variables...),
		Revision:  t.revision,
		CreatedAt: t.createdAt,
		UpdatedAt: t.updatedAt,
	}
}

// RehydrateEmailTemplate восстанавливает шаблон из сохранённого состояния,
// проверяя инварианты; противоречивый снимок отвергается. События не эмитятся.
func RehydrateEmailTemplate(s EmailTemplateSnapshot) (*EmailTemplate, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := validateTemplateFields(strings.TrimSpace(s.Name), strings.TrimSpace(s.Subject), strings.TrimSpace(s.Body)); err != nil {
		return nil, fmt.Errorf("%w: шаблон %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	variables, err := normalizeVariables(s.Variables)
	if err != nil {
		return nil, fmt.Errorf("%w: шаблон %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if err := validatePlaceholders(strings.TrimSpace(s.Subject), strings.TrimSpace(s.Body), variables); err != nil {
		return nil, fmt.Errorf("%w: шаблон %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if s.Revision < 1 {
		return nil, fmt.Errorf("%w: шаблон %s: ревизия %d", ErrInvalidSnapshot, s.ID, s.Revision)
	}
	return &EmailTemplate{
		id:        s.ID,
		name:      strings.TrimSpace(s.Name),
		subject:   strings.TrimSpace(s.Subject),
		body:      strings.TrimSpace(s.Body),
		variables: variables,
		revision:  s.Revision,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
	}, nil
}
