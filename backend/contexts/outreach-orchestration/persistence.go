// Хранилище сервиса outreach-orchestration, публикуемое composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через эту фабрику, как
// audience, billing и request-fulfillment публикуют NewPostgresPersistence.
package outreachorchestration

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/postgres"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// CampaignStore — набор портов хранилища кампаний, потребляемых срезом.
//
// Пакет сборки соединяет три независимых порта над одним хранилищем: репозиторий
// загрузки окна черновиков (пакетный путь и запросы), перечисление кампаний
// (движок последовательностей) и полноценный CampaignRepository. Реализация
// (in-memory или PostgreSQL) удовлетворяет всем трём сразу, поэтому срез зависит
// от их объединения, а не от конкретного адаптера.
type CampaignStore interface {
	ports.DraftWindowRepository
	ports.CampaignLister
}

// Persistence — набор адаптеров хранилища контекста, впрыскиваемых в Config.
// Нулевое значение означает dev-режим: сборка берёт in-memory адаптеры.
type Persistence struct {
	// Campaigns — хранилище кампаний: агрегат, окно черновиков и перечисление
	// идентификаторов (PostgreSQL).
	Campaigns CampaignStore

	// UnitOfWork — транзакция: атомарная запись агрегата и его событий в outbox.
	UnitOfWork ports.UnitOfWork

	// Journal — журнал outbox со стороны публикации: неподтверждённые события
	// переживают рестарт и доставляются relay.
	Journal relay.Source
	// Ping проверяет доступность того же PostgreSQL-пула без чтения или записи
	// бизнес-данных. Отсутствие callback не считается успешной проверкой.
	Ping func(context.Context) error
	// Editorial — отдельный durable store для editorial campaign/decision; он
	// не является outbox approved-action.
	Editorial appeditorial.Store
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища
// outreach-orchestration поверх пула зелёной зоны: репозиторий кампаний,
// unit-of-work и журнал outbox работают в схеме outreach-orchestration. Миграции
// схемы применяет точка сборки (backend/platform/migrate).
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Campaigns:  postgres.New(pool),
		UnitOfWork: postgres.NewUnitOfWork(pool),
		Journal:    postgres.NewJournal(pool),
		Editorial:  postgres.NewEditorialStore(pool),
		Ping:       pool.Ping,
	}
}

// configured сообщает, задан ли хотя бы один адаптер хранилища. Нулевое значение
// означает, что процесс собран на in-memory адаптерах (dev).
func (p Persistence) configured() bool {
	return p.Campaigns != nil || p.UnitOfWork != nil || p.Journal != nil
}
