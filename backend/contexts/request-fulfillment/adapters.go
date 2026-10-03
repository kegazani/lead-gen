// Адаптеры хранилища сервиса request-fulfillment, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через эти
// конструкторы, как lead-management публикует NewPostgresRepositories.
package requestfulfillment

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Persistence — набор адаптеров хранилища заявок, впрыскиваемых в Config.
// Нулевое значение означает dev-режим: сборка берёт in-memory адаптеры.
type Persistence struct {
	// Repository — репозиторий заявок (PostgreSQL).
	Repository ports.RequestRepository
	// UnitOfWork — транзакция заявок: атомарная запись агрегата и outbox.
	UnitOfWork ports.UnitOfWork
	// Journal — журнал outbox: ожидающие публикации события переживают рестарт.
	Journal platformoutbox.Journal
	// DraftStore — хранилище черновиков писем оператора.
	DraftStore ports.EmailDraftStore
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища заявок поверх
// пула зелёной зоны: репозиторий, unit-of-work, журнал outbox и хранилище
// черновиков работают в схеме request-fulfillment.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Repository: postgres.NewRepository(pool),
		UnitOfWork: postgres.NewUnitOfWork(pool),
		Journal:    postgres.NewJournal(pool),
		DraftStore: postgres.NewDraftStore(pool),
	}
}
