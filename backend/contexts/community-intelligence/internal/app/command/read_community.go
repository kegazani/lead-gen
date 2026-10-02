package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// DefaultQuoteThreshold — размер сообщества, начиная с которого в выжимку
// попадают цитаты-примеры. Ниже порога короткая фраза позволяет узнать автора
// (design.md, Риски).
const DefaultQuoteThreshold = 10

// ReadCommunity — команда чтения разрешённого сообщества за период.
type ReadCommunity struct {
	// CommunityID — читаемое сообщество.
	CommunityID domain.CommunityID

	// TenantID — тенант-владелец.
	TenantID domain.TenantID

	// From, To — границы периода чтения.
	From time.Time
	To   time.Time

	// MaxMessages — верхняя граница числа сообщений; 0 — без ограничения.
	MaxMessages int

	// DigestID — идентификатор создаваемой выжимки. Пусто — назначает генератор.
	DigestID domain.DigestID

	// Actor — оператор или системный актор сбора.
	Actor domain.ActorID

	// At — время построения выжимки. Ноль — берётся из часов.
	At time.Time

	// CorrelationID — сквозной сценарий; пусто — берётся идентификатор выжимки.
	CorrelationID domain.CorrelationID

	// CausationID — команда или событие, вызвавшее чтение.
	CausationID domain.CausationID
}

// ReadCommunityResult — результат чтения.
type ReadCommunityResult struct {
	DigestID      domain.DigestID
	CommunityID   domain.CommunityID
	PeriodFrom    time.Time
	PeriodTo      time.Time
	TopicCount    int
	CommunitySize int
	Version       int

	// ParticipantsAvailable — доступен ли состав участников. Ложь означает
	// частичный результат, а не подмену оценкой.
	ParticipantsAvailable bool

	// Events — подтверждённые события домена.
	Events []domain.DomainEvent
}

// ReadCommunityHandler — use case чтения разрешённого сообщества.
type ReadCommunityHandler struct {
	registry       ports.CommunityRegistry
	reader         ports.CommunityReader
	digests        ports.DigestRepository
	outbox         ports.Outbox
	clock          ports.Clock
	ids            ports.IDGenerator
	quoteThreshold int
}

// NewReadCommunityHandler собирает use case из реестра, источника, хранилища
// выжимок, outbox, часов, генератора идентификаторов и порога цитат. Порог не
// положителен — применяется DefaultQuoteThreshold.
func NewReadCommunityHandler(
	registry ports.CommunityRegistry,
	reader ports.CommunityReader,
	digests ports.DigestRepository,
	outbox ports.Outbox,
	clock ports.Clock,
	ids ports.IDGenerator,
	quoteThreshold int,
) *ReadCommunityHandler {
	if quoteThreshold <= 0 {
		quoteThreshold = DefaultQuoteThreshold
	}
	return &ReadCommunityHandler{
		registry:       registry,
		reader:         reader,
		digests:        digests,
		outbox:         outbox,
		clock:          clock,
		ids:            ids,
		quoteThreshold: quoteThreshold,
	}
}

// Handle читает сообщество, строит обезличенную выжимку и сохраняет её вместе с
// событием community-digest-created.
//
// Авторы сообщений отделяются на границе: в домен попадают только текст и ссылка
// на сообщение. Если за период нет пригодных сообщений, выжимка не создаётся
// (domain.ErrNoMessages), а пустая выжимка с нулями — не выдаётся.
func (h *ReadCommunityHandler) Handle(ctx context.Context, cmd ReadCommunity) (ReadCommunityResult, error) {
	source, err := h.registry.FindByID(ctx, cmd.TenantID, cmd.CommunityID)
	if err != nil {
		return ReadCommunityResult{}, err
	}
	period, err := domain.NewDigestPeriod(cmd.From, cmd.To)
	if err != nil {
		return ReadCommunityResult{}, err
	}

	read, err := h.reader.ReadCommunity(ctx, ports.ReadRequest{
		Source:      source,
		Period:      period,
		MaxMessages: cmd.MaxMessages,
	})
	if err != nil {
		return ReadCommunityResult{}, err
	}

	candidates := make([]domain.Candidate, 0, len(read.Messages))
	for _, msg := range read.Messages {
		candidates = append(candidates, domain.Candidate{
			Text: msg.Text,
			Ref:  domain.MessageRef{Community: source.ID(), Message: msg.MessageID},
		})
	}
	topics, err := domain.BuildTopics(candidates, domain.BuildTopicsOptions{
		CommunitySize:  read.CommunitySize,
		QuoteThreshold: h.quoteThreshold,
	})
	if err != nil {
		return ReadCommunityResult{}, err
	}

	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	digestID := cmd.DigestID
	if digestID == "" && h.ids != nil {
		digestID = h.ids.NewDigestID()
	}
	correlation := cmd.CorrelationID
	if correlation == "" {
		correlation = domain.CorrelationID(digestID)
	}
	var eventID domain.EventID
	if h.ids != nil {
		eventID = h.ids.NewEventID()
	}

	digest, err := domain.NewDigest(domain.NewDigestParams{
		ID:            digestID,
		TenantID:      cmd.TenantID,
		Source:        source,
		Period:        period,
		Topics:        topics,
		CommunitySize: read.CommunitySize,
		At:            at,
		EventID:       eventID,
		CorrelationID: correlation,
		CausationID:   cmd.CausationID,
	})
	if err != nil {
		return ReadCommunityResult{}, err
	}
	if err := h.digests.Save(ctx, digest); err != nil {
		return ReadCommunityResult{}, err
	}
	events := digest.DomainEvents()
	if h.outbox != nil {
		if err := h.outbox.Append(ctx, events...); err != nil {
			return ReadCommunityResult{}, err
		}
	}
	digest.PullDomainEvents()

	return ReadCommunityResult{
		DigestID:              digest.ID(),
		CommunityID:           source.ID(),
		PeriodFrom:            period.From(),
		PeriodTo:              period.To(),
		TopicCount:            len(topics),
		CommunitySize:         read.CommunitySize,
		Version:               digest.Version(),
		ParticipantsAvailable: read.ParticipantsAvailable,
		Events:                events,
	}, nil
}
