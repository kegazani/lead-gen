package query

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// ErrTenantMismatch — выжимка принадлежит другому тенанту. Запрос не раскрывает
// существование чужой выжимки.
var ErrTenantMismatch = errors.New("запрос: выжимка принадлежит другому тенанту")

// SourceRef — ссылка на сообщение-источник пункта выжимки. Автора не несёт.
type SourceRef struct {
	CommunityID string
	MessageID   string
}

// TopicView — пункт выжимки: тема, частота, обезличенные примеры и источники.
type TopicView struct {
	Name     string
	Mentions int
	Quotes   []string
	Sources  []SourceRef
}

// DigestView — обезличенное представление выжимки для выдачи.
type DigestView struct {
	DigestID       string
	CommunityID    string
	CommunityTitle string
	PeriodFrom     time.Time
	PeriodTo       time.Time
	CommunitySize  int
	CreatedAt      time.Time
	Topics         []TopicView
}

// GetDigest — запрос выжимки по идентификатору.
type GetDigest struct {
	DigestID domain.DigestID
	TenantID domain.TenantID
}

// GetDigestHandler — use case чтения выжимки.
type GetDigestHandler struct {
	digests ports.DigestRepository
}

// NewGetDigestHandler собирает use case из хранилища выжимок.
func NewGetDigestHandler(digests ports.DigestRepository) *GetDigestHandler {
	return &GetDigestHandler{digests: digests}
}

// Handle возвращает обезличенную выжимку. Выжимка другого тенанта отвечает
// ErrTenantMismatch.
func (h *GetDigestHandler) Handle(ctx context.Context, q GetDigest) (DigestView, error) {
	digest, err := h.digests.FindByID(ctx, q.DigestID)
	if err != nil {
		return DigestView{}, err
	}
	if q.TenantID != "" && digest.TenantID() != q.TenantID {
		return DigestView{}, ErrTenantMismatch
	}
	return ToView(digest), nil
}

// ToView переводит агрегат в обезличенное представление. Авторов и состава
// участников здесь нет по построению домена.
func ToView(digest *domain.CommunityDigest) DigestView {
	topics := make([]TopicView, 0, len(digest.Topics()))
	for _, topic := range digest.Topics() {
		quotes := make([]string, 0, len(topic.Quotes()))
		for _, quote := range topic.Quotes() {
			quotes = append(quotes, quote.Text())
		}
		sources := make([]SourceRef, 0, len(topic.Sources()))
		for _, ref := range topic.Sources() {
			sources = append(sources, SourceRef{
				CommunityID: ref.Community.String(),
				MessageID:   ref.Message.String(),
			})
		}
		topics = append(topics, TopicView{
			Name:     topic.Name(),
			Mentions: topic.Mentions(),
			Quotes:   quotes,
			Sources:  sources,
		})
	}
	period := digest.Period()
	return DigestView{
		DigestID:       digest.ID().String(),
		CommunityID:    digest.Source().ID().String(),
		CommunityTitle: digest.Source().Title(),
		PeriodFrom:     period.From(),
		PeriodTo:       period.To(),
		CommunitySize:  digest.CommunitySize(),
		CreatedAt:      digest.CreatedAt(),
		Topics:         topics,
	}
}
