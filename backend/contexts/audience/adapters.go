// Адаптеры хранилища сервиса audience, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через эти
// конструкторы, как lead-management публикует NewPostgresRepositories, а
// request-fulfillment — NewPostgresPersistence.
package audience

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/audience/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/audience/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/readiness"
)

// Persistence — набор адаптеров хранилища audience, впрыскиваемых в Config.
// Нулевое значение означает dev-режим: сборка берёт in-memory адаптеры.
type Persistence struct {
	// Segments — репозиторий сегментов (PostgreSQL).
	Segments ports.SegmentRepository
	// Snapshots — репозиторий снимков аудитории (PostgreSQL).
	Snapshots ports.AudienceSnapshotRepository
	// UnitOfWork — транзакция: атомарная запись агрегатов и outbox.
	UnitOfWork ports.UnitOfWork
	// Journal — журнал outbox: неопубликованные события переживают рестарт.
	Journal platformoutbox.Journal
	// DatabaseProbe проверяет уже собранный пул без бизнес-операций.
	DatabaseProbe readiness.Probe
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища audience поверх
// пула зелёной зоны: репозитории сегментов и снимков, unit-of-work и журнал
// outbox работают в схеме audience.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Segments:   postgres.NewSegmentRepository(pool),
		Snapshots:  postgres.NewSnapshotRepository(pool),
		UnitOfWork: postgres.NewUnitOfWork(pool),
		Journal:    postgres.NewJournal(pool),
		DatabaseProbe: func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := pool.Ping(ctx); err != nil {
				return readiness.ProbeResult{}, err
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		},
	}
}

// configured сообщает, задан ли хотя бы один адаптер хранилища. Нулевое значение
// означает, что процесс собран на in-memory адаптерах (dev).
func (p Persistence) configured() bool {
	return p.Segments != nil || p.Snapshots != nil || p.UnitOfWork != nil || p.Journal != nil
}
