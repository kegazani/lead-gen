package ports

import (
	"context"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// CampaignLister — аддитивное расширение хранилища кампаний: перечисление
// идентификаторов кампаний для роли chains. Сам CampaignRepository загружает
// агрегат по известному идентификатору и перечислять кампании не умеет.
//
// Порт возвращает только идентификаторы: движок последовательностей загружает
// каждую кампанию в отдельной транзакции и не держит весь набор в памяти.
type CampaignLister interface {
	// ListCampaignIDs возвращает идентификаторы всех сохранённых кампаний.
	// Порядок не определён.
	ListCampaignIDs(ctx context.Context) ([]domain.CampaignID, error)
}
