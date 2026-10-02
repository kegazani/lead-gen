package domain

import (
	"fmt"
	"time"
)

// CommunityDigest — aggregate root контекста community-intelligence: обезличенная
// выжимка разрешённого Telegram-сообщества (architecture/domain-catalog.yaml,
// aggregates: community-digest).
//
// Инварианты:
//
//	I1 — выжимка строится только по разрешённому источнику (policy
//	     authorized-community-read);
//	I2 — период задан и согласован;
//	I3 — выжимка содержит хотя бы одну тему: сообщений нет — выжимка не создаётся;
//	I4 — выжимка не содержит прямых идентификаторов авторов: тема хранит лишь
//	     обезличенные цитаты и ссылки на сообщения (policy digest-anonymization).
//
// Поля закрыты: состояние меняется только конструктором. version — токен
// оптимистичной блокировки; создание даёт версию 1.
type CommunityDigest struct {
	id            DigestID
	tenantID      TenantID
	source        CommunitySource
	period        DigestPeriod
	topics        []Topic
	communitySize int
	createdAt     time.Time
	version       int
	events        []DomainEvent
}

// NewDigestParams — данные создания выжимки.
type NewDigestParams struct {
	ID            DigestID
	TenantID      TenantID
	Source        CommunitySource
	Period        DigestPeriod
	Topics        []Topic
	CommunitySize int
	At            time.Time
	EventID       EventID
	CorrelationID CorrelationID
	CausationID   CausationID
}

// NewDigest создаёт выжимку, проверяя инварианты, и эмитит
// community-digest-created.
func NewDigest(p NewDigestParams) (*CommunityDigest, error) {
	if err := p.ID.validate(); err != nil {
		return nil, err
	}
	if err := p.TenantID.validate(); err != nil {
		return nil, err
	}
	if !p.Source.IsAuthorized() || p.Source.ID() == "" {
		return nil, ErrSourceNotAuthorized
	}
	if p.Period.From().IsZero() || p.Period.To().IsZero() {
		return nil, fmt.Errorf("%w: период не задан", ErrInvalidPeriod)
	}
	if len(p.Topics) == 0 {
		return nil, ErrNoMessages
	}
	if p.At.IsZero() {
		return nil, ErrMissingTime
	}
	if err := p.EventID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMissingEventID, "community-digest-created")
	}

	topics := make([]Topic, len(p.Topics))
	copy(topics, p.Topics)

	digest := &CommunityDigest{
		id:            p.ID,
		tenantID:      p.TenantID,
		source:        p.Source,
		period:        p.Period,
		topics:        topics,
		communitySize: p.CommunitySize,
		createdAt:     p.At,
		version:       1,
	}
	digest.events = append(digest.events, CommunityDigestCreated{
		EventHeader: EventHeader{
			EventID:       p.EventID,
			OccurredAt:    p.At,
			TenantID:      p.TenantID,
			CorrelationID: p.CorrelationID,
			CausationID:   p.CausationID,
		},
		DigestID:      p.ID,
		CommunityID:   p.Source.ID(),
		PeriodFrom:    p.Period.From(),
		PeriodTo:      p.Period.To(),
		TopicCount:    len(topics),
		CommunitySize: p.CommunitySize,
	})
	return digest, nil
}

// ID возвращает идентификатор выжимки.
func (d *CommunityDigest) ID() DigestID { return d.id }

// TenantID возвращает тенант-владельца выжимки.
func (d *CommunityDigest) TenantID() TenantID { return d.tenantID }

// Source возвращает источник выжимки.
func (d *CommunityDigest) Source() CommunitySource { return d.source }

// Period возвращает период выжимки.
func (d *CommunityDigest) Period() DigestPeriod { return d.period }

// Topics возвращает копию тем выжимки.
func (d *CommunityDigest) Topics() []Topic {
	out := make([]Topic, len(d.topics))
	copy(out, d.topics)
	return out
}

// CommunitySize возвращает размер сообщества на момент чтения.
func (d *CommunityDigest) CommunitySize() int { return d.communitySize }

// CreatedAt возвращает время построения выжимки.
func (d *CommunityDigest) CreatedAt() time.Time { return d.createdAt }

// Version возвращает версию агрегата (токен оптимистичной блокировки).
func (d *CommunityDigest) Version() int { return d.version }

// DomainEvents возвращает копию неподтверждённых событий.
func (d *CommunityDigest) DomainEvents() []DomainEvent {
	out := make([]DomainEvent, len(d.events))
	copy(out, d.events)
	return out
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (d *CommunityDigest) PullDomainEvents() []DomainEvent {
	out := d.DomainEvents()
	d.events = nil
	return out
}

// CommunityDigestSnapshot — полное сохранённое состояние aggregate root.
type CommunityDigestSnapshot struct {
	ID            DigestID
	TenantID      TenantID
	Source        CommunitySourceSnapshot
	Period        DigestPeriodSnapshot
	Topics        []TopicSnapshot
	CommunitySize int
	CreatedAt     time.Time
	Version       int
}

// Snapshot возвращает состояние агрегата для сохранения.
func (d *CommunityDigest) Snapshot() CommunityDigestSnapshot {
	topics := make([]TopicSnapshot, 0, len(d.topics))
	for i := range d.topics {
		topics = append(topics, d.topics[i].Snapshot())
	}
	return CommunityDigestSnapshot{
		ID:            d.id,
		TenantID:      d.tenantID,
		Source:        d.source.Snapshot(),
		Period:        d.period.Snapshot(),
		Topics:        topics,
		CommunitySize: d.communitySize,
		CreatedAt:     d.createdAt,
		Version:       d.version,
	}
}

// RehydrateDigest восстанавливает агрегат из сохранённого состояния, проверяя
// инварианты. События не эмитятся: восстановление не является новым фактом
// домена.
func RehydrateDigest(s CommunityDigestSnapshot) (*CommunityDigest, error) {
	if err := s.ID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	source, err := RehydrateCommunitySource(s.Source)
	if err != nil {
		return nil, err
	}
	period, err := RehydrateDigestPeriod(s.Period)
	if err != nil {
		return nil, err
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: выжимка %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	if s.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: выжимка %s без времени", ErrInvalidSnapshot, s.ID)
	}
	if len(s.Topics) == 0 {
		return nil, fmt.Errorf("%w: выжимка %s без тем", ErrInvalidSnapshot, s.ID)
	}
	topics := make([]Topic, 0, len(s.Topics))
	for _, ts := range s.Topics {
		topic, err := RehydrateTopic(ts)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return &CommunityDigest{
		id:            s.ID,
		tenantID:      s.TenantID,
		source:        source,
		period:        period,
		topics:        topics,
		communitySize: s.CommunitySize,
		createdAt:     s.CreatedAt,
		version:       s.Version,
	}, nil
}

// String возвращает краткое представление выжимки для логов. Содержания и
// авторов здесь нет.
func (d *CommunityDigest) String() string {
	return fmt.Sprintf("digest %s сообщества %s (%s..%s), тем: %d",
		d.id, d.source.ID(), d.period.From().UTC().Format(time.RFC3339),
		d.period.To().UTC().Format(time.RFC3339), len(d.topics))
}

// Проверка соответствия интерфейсу публикации событий на этапе компиляции.
var _ EventProducer = (*CommunityDigest)(nil)
