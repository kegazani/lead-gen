package ports

import "context"

// RequestCancellationFence хранит owner-owned tombstone заявки и связь с
// созданной кампанией. Реализация вызывается только внутри UnitOfWork.
type RequestCancellationFence interface {
	Cancel(ctx context.Context, tenantID, requestID string) error
	RegisterHandoff(ctx context.Context, tenantID, requestID, campaignID string) (cancelled bool, err error)
	CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error)
}

// TransactionRequestCancellationFence — необязательная для общих транзакций
// способность UnitOfWork, предоставляемая поддерживающими её адаптерами.
type TransactionRequestCancellationFence interface {
	RequestCancellationFence() RequestCancellationFence
}
