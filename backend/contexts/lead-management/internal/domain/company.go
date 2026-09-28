package domain

import (
	"fmt"
	"strings"
	"time"
)

// CompanyID — идентификатор компании. В срезе равен нормализованному домену
// сайта (company-identity каталога); нормализацию выполняет прикладной слой,
// домен лишь требует непустое значение.
type CompanyID string

// validate проверяет непустоту идентификатора.
func (id CompanyID) validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return ErrEmptyCompanyID
	}
	return nil
}

// String возвращает идентификатор строкой.
func (id CompanyID) String() string { return string(id) }

// AttributedValue — значение поля карточки с происхождением. Пустое значение
// означает «поле не найдено»; заполненное обязано нести происхождение.
type AttributedValue struct {
	Value      string
	Provenance Provenance
}

// validate проверяет согласованность значения и происхождения.
func (v AttributedValue) validate() error {
	if strings.TrimSpace(v.Value) == "" {
		return nil
	}
	return v.Provenance.validate()
}

// Company — aggregate root lead-profile (root entity company). Карточка компании
// с названием, описанием и соцсетями; каждое поле несёт происхождение.
//
// Инвариант: нормализованный идентификатор компании не меняется — id задаётся
// только конструктором и восстановлением.
type Company struct {
	id          CompanyID
	name        AttributedValue
	description AttributedValue
	socials     []AttributedValue
	hidden      bool
	status      LeadStatus
	createdAt   time.Time
	updatedAt   time.Time
	events      []DomainEvent
}

// CreateCompanyParams — параметры создания карточки компании.
type CreateCompanyParams struct {
	// Domain — нормализованный домен сайта (идентификатор компании).
	Domain string
	// Name — название компании (обязательно, с происхождением).
	Name AttributedValue
	// Description — описание (необязательно).
	Description AttributedValue
	// Socials — соцсети (необязательно).
	Socials []AttributedValue
	// CreatedAt — момент создания.
	CreatedAt time.Time
}

// CreateCompany создаёт карточку компании. Название и происхождение обязательны;
// прочие поля необязательны, но каждое заполненное несёт происхождение.
func CreateCompany(params CreateCompanyParams) (*Company, error) {
	id := CompanyID(strings.TrimSpace(params.Domain))
	if err := id.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Name.Value) == "" {
		return nil, fmt.Errorf("%w: компания %s", ErrEmptyCompanyName, id)
	}
	if err := params.Name.validate(); err != nil {
		return nil, err
	}
	if err := params.Description.validate(); err != nil {
		return nil, err
	}
	for _, social := range params.Socials {
		if err := social.validate(); err != nil {
			return nil, err
		}
	}
	now := params.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c := &Company{
		id:          id,
		name:        params.Name,
		description: params.Description,
		socials:     append([]AttributedValue(nil), params.Socials...),
		status:      LeadStatus{Stage: StageNotWarmed},
		createdAt:   now,
		updatedAt:   now,
	}
	c.events = append(c.events, LeadProfileEnriched{CompanyID: id, At: now.Format(time.RFC3339)})
	return c, nil
}

// ID возвращает идентификатор компании.
func (c *Company) ID() CompanyID { return c.id }

// Name возвращает название компании.
func (c *Company) Name() AttributedValue { return c.name }

// Description возвращает описание компании.
func (c *Company) Description() AttributedValue { return c.description }

// Socials возвращает соцсети компании.
func (c *Company) Socials() []AttributedValue { return append([]AttributedValue(nil), c.socials...) }

// CreatedAt возвращает момент создания.
func (c *Company) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt возвращает момент последнего изменения.
func (c *Company) UpdatedAt() time.Time { return c.updatedAt }

// Hidden сообщает, скрыта ли запись в витрине.
func (c *Company) Hidden() bool { return c.hidden }

// Status возвращает статус лида (стадия и срок snooze).
func (c *Company) Status() LeadStatus { return c.status.Snapshot() }

// ChangeStage переносит лида на другую стадию воронки. Стадия вне согласованного
// набора не применяется; срок snooze сохраняется (возврат в пул — отдельная
// операция). Фиксация перехода (время, автор) — операционная запись app-слоя,
// домен лишь меняет атрибут.
func (c *Company) ChangeStage(stage Stage, at time.Time) error {
	if !stage.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidStage, stage)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.status.Stage = stage
	c.updatedAt = at
	return nil
}

// Snooze откладывает лида до срока until: до него лид скрыт из активной воронки.
func (c *Company) Snooze(until time.Time, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	cloned := until
	c.status.SnoozeUntil = &cloned
	c.updatedAt = at
}

// ReturnToPool возвращает отложенного лида в активную воронку (снимает snooze).
// Операция идемпотентна: если лид не был отложен, ничего не меняется.
func (c *Company) ReturnToPool(at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.status.SnoozeUntil = nil
	c.updatedAt = at
}

// Hide скрывает запись в витрине. Операция идемпотентна.
func (c *Company) Hide(at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.hidden = true
	c.updatedAt = at
}

// Enrich дополняет карточку отсутствующими полями и не перетирает уже
// заполненные: повторный обход того же сайта дополняет, а не заменяет.
// Новые соцсети добавляются без дублей по значению.
func (c *Company) Enrich(name, description AttributedValue, socials []AttributedValue, at time.Time) {
	if c.name.Value == "" && strings.TrimSpace(name.Value) != "" {
		c.name = name
	}
	if c.description.Value == "" && strings.TrimSpace(description.Value) != "" {
		c.description = description
	}
	c.mergeSocials(socials)
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.updatedAt = at
	c.events = append(c.events, LeadProfileEnriched{CompanyID: c.id, At: at.Format(time.RFC3339)})
}

// mergeSocials добавляет соцсети, которых ещё нет, сохраняя порядок.
func (c *Company) mergeSocials(socials []AttributedValue) {
	seen := make(map[string]struct{}, len(c.socials))
	for _, s := range c.socials {
		seen[strings.TrimSpace(s.Value)] = struct{}{}
	}
	for _, s := range socials {
		key := strings.TrimSpace(s.Value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		c.socials = append(c.socials, s)
	}
}

// Events возвращает и очищает буфер событий агрегата.
func (c *Company) Events() []DomainEvent {
	events := c.events
	c.events = nil
	return events
}
