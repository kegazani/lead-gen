package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// ErrCampaignNotFound — кампания отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища, тогда как domain.ErrDraftNotFound
// описывает отсутствие черновика внутри уже загруженного агрегата.
var ErrCampaignNotFound = errors.New("порты: кампания не найдена")

// CampaignRepository — порт сохранения и загрузки aggregate root campaign.
//
// Репозиторий существует только для aggregate root. Сущности action-draft и
// operator-decision не сохраняются отдельными вызовами: они входят в состав
// кампании и записываются вместе с ней. Порт не знает о драйвере БД, схеме
// таблиц, SQL и типах внешних систем — его сигнатуры оперируют только
// доменными типами.
//
// Репозиторий не управляет транзакцией: сохранение выполняется в рамках
// транзакции, открытой unit-of-work (см. unit_of_work.go), поэтому commit и
// rollback остаются за вызывающим use case.
//
// Загрузка окна черновиков — отдельное аддитивное расширение этого порта
// (см. draft_window.go, DraftWindowRepository): сам CampaignRepository не
// меняется, чтобы не ломать существующие реализации Save/FindByID.
type CampaignRepository interface {
	// Save сохраняет агрегат целиком. Повторный вызов для той же кампании
	// обновляет ранее сохранённое состояние (upsert по идентификатору),
	// не создавая вторую кампанию.
	Save(ctx context.Context, campaign *domain.Campaign) error

	// FindByID загружает кампанию по идентификатору. Если кампании нет,
	// возвращается ошибка, для которой errors.Is(err, ErrCampaignNotFound)
	// истинна; загруженный агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.CampaignID) (*domain.Campaign, error)
}
