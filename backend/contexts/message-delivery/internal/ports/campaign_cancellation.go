package ports

import "context"

import "errors"

// ErrCampaignCancelled указывает, что команда fenced до начала отправки.
var ErrCampaignCancelled = errors.New("message-delivery: кампания отменена")

// CampaignCanceller записывает durable fence для кампании. Повторная отмена
// идемпотентна; после успешного возврата новые reservations запрещены.
type CampaignCanceller interface {
	CancelCampaign(ctx context.Context, tenantID, campaignID string) error
}

// CampaignCancellationQueue fences admission and removes pending/leased items.
type CampaignCancellationQueue interface {
	CampaignCanceller
}

type CampaignFenceReader interface {
	CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error)
}

// CampaignFenceBinder связывает локальный UoW с источником cancellation fence
// при сборке in-memory контура. PostgreSQL использует проверку внутри tx.
type CampaignFenceBinder interface {
	BindCampaignFence(CampaignFenceReader)
}
