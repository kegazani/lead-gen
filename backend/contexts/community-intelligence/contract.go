package communityintelligence

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/community-intelligence/internal/app/command"
	"lead-gen/backend/contexts/community-intelligence/internal/app/query"
	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
	"lead-gen/contracts/community-intelligence/communitydigest"
	"lead-gen/contracts/community-intelligence/communityreadcommand"
)

// NewCommunityDigest собирает опубликованный контракт community-digest поверх
// собранного среза: чтение обезличенной выжимки.
//
// Это публичная фабрика владельца выжимки: точка сборки не имеет доступа к
// внутренним пакетам контекста, поэтому прямой адаптер контракта объявлен здесь.
// При co-location он вызывается в процессе; при разделении сервисов та же форма
// даётся сетевым клиентом.
func NewCommunityDigest(slice *Slice) communitydigest.Port {
	if slice == nil {
		return communitydigest.InProc(communitydigest.InProcHandler{})
	}
	return communitydigest.InProc(communitydigest.InProcHandler{
		GetFunc: func(ctx context.Context, q communitydigest.GetQuery) (communitydigest.DigestView, error) {
			view, err := slice.GetDigest.Handle(ctx, query.GetDigest{
				DigestID: domain.DigestID(q.DigestID),
				TenantID: domain.TenantID(q.TenantID),
			})
			if err != nil {
				return communitydigest.DigestView{}, communityDigestError(err)
			}
			return communityDigestView(view), nil
		},
	})
}

// communityDigestView переводит представление среза в опубликованную форму.
func communityDigestView(view query.DigestView) communitydigest.DigestView {
	topics := make([]communitydigest.TopicView, 0, len(view.Topics))
	for _, topic := range view.Topics {
		sources := make([]communitydigest.SourceRef, 0, len(topic.Sources))
		for _, ref := range topic.Sources {
			sources = append(sources, communitydigest.SourceRef{
				CommunityID: ref.CommunityID,
				MessageID:   ref.MessageID,
			})
		}
		quotes := make([]string, len(topic.Quotes))
		copy(quotes, topic.Quotes)
		topics = append(topics, communitydigest.TopicView{
			Name:     topic.Name,
			Mentions: topic.Mentions,
			Quotes:   quotes,
			Sources:  sources,
		})
	}
	return communitydigest.DigestView{
		DigestID:       view.DigestID,
		CommunityID:    view.CommunityID,
		CommunityTitle: view.CommunityTitle,
		PeriodFrom:     view.PeriodFrom,
		PeriodTo:       view.PeriodTo,
		CommunitySize:  view.CommunitySize,
		CreatedAt:      view.CreatedAt,
		Topics:         topics,
	}
}

// communityDigestError переводит ошибку запроса в исход опубликованного
// контракта: отсутствие выжимки (в том числе чужого тенанта) — ErrNotFound,
// прочее — ErrUnavailable.
func communityDigestError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrDigestNotFound), errors.Is(err, query.ErrTenantMismatch):
		return fmt.Errorf("%w: %v", communitydigest.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", communitydigest.ErrUnavailable, err)
	}
}

// NewCommunityReadCommand собирает опубликованный контракт
// community-read-command поверх собранного среза: команда чтения разрешённого
// сообщества.
func NewCommunityReadCommand(slice *Slice) communityreadcommand.Port {
	if slice == nil {
		return communityreadcommand.InProc(communityreadcommand.InProcHandler{})
	}
	return communityreadcommand.InProc(communityreadcommand.InProcHandler{
		ReadFunc: func(ctx context.Context, cmd communityreadcommand.ReadCommand) (communityreadcommand.ReadResult, error) {
			result, err := slice.ReadCommunity.Handle(ctx, command.ReadCommunity{
				CommunityID:   domain.CommunityID(cmd.CommunityID),
				TenantID:      domain.TenantID(cmd.TenantID),
				From:          cmd.From,
				To:            cmd.To,
				MaxMessages:   cmd.MaxMessages,
				Actor:         domain.ActorID(cmd.Actor),
				CorrelationID: domain.CorrelationID(cmd.CorrelationID),
			})
			if err != nil {
				return communityreadcommand.ReadResult{}, communityReadCommandError(err)
			}
			return communityreadcommand.ReadResult{
				DigestID:    result.DigestID.String(),
				CommunityID: result.CommunityID.String(),
				PeriodFrom:  result.PeriodFrom,
				PeriodTo:    result.PeriodTo,
				TopicCount:  result.TopicCount,
			}, nil
		},
	})
}

// communityReadCommandError переводит ошибку сценария чтения в исход
// опубликованного контракта: доменный отказ и ненайденное сообщество —
// ErrRejected, отсутствие сессии и сбой источника — ErrUnavailable.
func communityReadCommandError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrCommunityNotFound),
		errors.Is(err, domain.ErrSourceNotAuthorized),
		errors.Is(err, domain.ErrBotSourceRejected),
		errors.Is(err, domain.ErrUnknownSourceKind),
		errors.Is(err, domain.ErrEmptySourceTitle),
		errors.Is(err, domain.ErrInvalidPeriod),
		errors.Is(err, domain.ErrNoMessages),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingTime):
		return fmt.Errorf("%w: %v", communityreadcommand.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", communityreadcommand.ErrUnavailable, err)
	}
}
