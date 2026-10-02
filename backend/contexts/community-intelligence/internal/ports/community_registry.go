package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// ErrCommunityNotFound — сообщество не зарегистрировано как разрешённое или
// принадлежит другому тенанту. Существование чужого сообщества этим отказом не
// раскрывается.
var ErrCommunityNotFound = errors.New("порты: сообщество не найдено в реестре")

// CommunityRegistry — порт доступа к реестру разрешённых сообществ. Реестр хранит
// только источники, прошедшие policy authorized-community-read.
type CommunityRegistry interface {
	// FindByID возвращает разрешённый источник сообщества. Если сообщества нет,
	// возвращается ошибка, для которой истинно errors.Is(err,
	// ErrCommunityNotFound).
	FindByID(ctx context.Context, tenant domain.TenantID, id domain.CommunityID) (domain.CommunitySource, error)
}

// TrackedCommunity — отслеживаемое сообщество: что читать и в каких границах.
type TrackedCommunity struct {
	// CommunityID — идентификатор сообщества.
	CommunityID domain.CommunityID

	// TenantID — тенант-владелец.
	TenantID domain.TenantID

	// MaxMessages — верхняя граница числа сообщений; 0 — без ограничения.
	MaxMessages int
}

// CommunityTracker — порт списка отслеживаемых сообществ для роли schedule.
// Регулярный сбор обходит именно этот список (spec, «Непрерывное чтение по
// расписанию»).
type CommunityTracker interface {
	// Tracked возвращает отслеживаемые сообщества. Порядок — по идентификатору,
	// чтобы сбор был воспроизводим.
	Tracked(ctx context.Context) ([]TrackedCommunity, error)
}
