// Адаптеры хранилища сервиса engagement-observation, публикуемые composition
// root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через этот
// конструктор, как request-fulfillment публикует NewPostgresPersistence.
package engagementobservation

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Persistence — набор адаптеров хранилища контекста, впрыскиваемых в Config.
// Нулевое значение означает dev-режим: сборка берёт in-memory адаптеры.
type Persistence struct {
	// Repository — хранилище событий взаимодействия (PostgreSQL).
	Repository ports.EngagementRepository
	// Ledger — журнал дедупликации сырых сигналов.
	Ledger ports.SignalLedger
	// Links — реестр трекинг-ссылок.
	Links ports.TransitionLinks
	// UnitOfWork — транзакция контекста: атомарная запись события, отметки
	// дедупликации, ссылки и outbox.
	UnitOfWork ports.UnitOfWork
	// Journal — журнал outbox: ожидающие публикации события переживают рестарт.
	Journal platformoutbox.Journal
	// Ping выполняет пассивную проверку соединения с тем же PostgreSQL-пулом.
	Ping func(context.Context) error
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища контекста поверх
// пула зелёной зоны: хранилище событий, журнал дедупликации, реестр ссылок,
// unit-of-work и журнал outbox работают в схеме engagement-observation.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Repository: postgres.New(pool),
		Ledger:     postgres.NewLedger(pool),
		Links:      postgres.NewLinks(pool),
		UnitOfWork: postgres.NewUnitOfWork(pool),
		Journal:    postgres.NewJournal(pool),
		Ping:       pool.Ping,
	}
}
