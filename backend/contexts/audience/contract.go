package audience

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
	"lead-gen/contracts/audience/audiencesnapshot"
	"lead-gen/contracts/audience/segmentmembership"
)

// Связывание опубликованных контрактов контекста audience.
//
// Контракты выражены интерфейсами в отдельном модуле lead-gen/contracts/audience,
// а не пакетами владельца: потребитель держит audiencesnapshot.Port или
// segmentmembership.Port и не видит внутренних пакетов контекста. Точка сборки
// сервиса (этот пакет) выбирает, чем связать потребителя с реализацией владельца:
// прямым вызовом при co-location или сетевым клиентом при разделении сервисов
// (design.md, Решение 2).
//
// Точка сборки не имеет доступа к внутренним пакетам контекста, поэтому адаптеры
// контрактов объявлены здесь. При co-location они вызываются в процессе; при
// разделении сервисов ту же форму даёт сетевой клиент.

// NewAudienceSnapshot собирает опубликованный контракт audience-snapshot поверх
// собранного среза: запрос снимка аудитории по идентификатору.
func NewAudienceSnapshot(slice *Slice) audiencesnapshot.Port {
	if slice == nil {
		return audiencesnapshot.InProc(audiencesnapshot.InProcHandler{})
	}
	return audiencesnapshot.InProc(audiencesnapshot.InProcHandler{
		FetchFunc: func(ctx context.Context, env audiencesnapshot.Envelope, snapshotID string) (audiencesnapshot.AudienceSnapshot, error) {
			env.Name = audiencesnapshot.ContractName
			env.SchemaVersion = audiencesnapshot.SchemaVersion
			view, err := slice.GetSnapshot.Handle(ctx, query.GetAudienceSnapshot{
				SnapshotID: domain.SnapshotID(snapshotID),
				TenantID:   domain.TenantID(env.TenantID),
			})
			if err != nil {
				return audiencesnapshot.AudienceSnapshot{}, audienceSnapshotError(err)
			}
			return audiencesnapshot.AudienceSnapshot{
				Envelope:    env,
				SnapshotID:  view.SnapshotID,
				SegmentID:   view.SegmentID,
				Version:     view.Version,
				MemberCount: int64(view.MemberCount),
				GeneratedAt: view.GeneratedAt,
			}, nil
		},
	})
}

// NewSegmentMembership собирает опубликованный контракт segment-membership поверх
// собранного среза: запрос состава сегмента аудитории.
func NewSegmentMembership(slice *Slice) segmentmembership.Port {
	if slice == nil {
		return segmentmembership.InProc(segmentmembership.InProcHandler{})
	}
	return segmentmembership.InProc(segmentmembership.InProcHandler{
		MembershipFunc: func(ctx context.Context, env segmentmembership.Envelope, segmentID string) (segmentmembership.SegmentMembership, error) {
			env.Name = segmentmembership.ContractName
			env.SchemaVersion = segmentmembership.SchemaVersion
			view, err := slice.SegmentMembers.Handle(ctx, query.SegmentMembers{
				SegmentID: domain.SegmentID(segmentID),
				TenantID:  domain.TenantID(env.TenantID),
			})
			if err != nil {
				return segmentmembership.SegmentMembership{}, segmentMembershipError(err)
			}
			return segmentmembership.SegmentMembership{
				Envelope:    env,
				SegmentID:   view.SegmentID,
				Version:     int64(view.Version),
				MemberCount: int64(view.MemberCount),
				Members:     memberRefs(view.Members),
				ChangedAt:   view.LastChangedAt,
			}, nil
		},
	})
}

// memberRefs переводит состав сегмента в ссылки контракта.
func memberRefs(members []query.MemberView) []string {
	refs := make([]string, 0, len(members))
	for _, member := range members {
		refs = append(refs, member.LeadID)
	}
	return refs
}

// audienceSnapshotError переводит ошибку сценария в исход опубликованного
// контракта: отсутствие снимка (в том числе чужого тенанта) — ErrNotFound,
// прочее — ErrUnavailable. Ошибка не выдаётся за успех.
func audienceSnapshotError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSnapshotNotFound):
		return fmt.Errorf("%w: %v", audiencesnapshot.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", audiencesnapshot.ErrUnavailable, err)
	}
}

// segmentMembershipError переводит ошибку сценария в исход опубликованного
// контракта.
func segmentMembershipError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSegmentNotFound):
		return fmt.Errorf("%w: %v", segmentmembership.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", segmentmembership.ErrUnavailable, err)
	}
}
