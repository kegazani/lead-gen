// Package postgres реализует порты контекста agent-orchestration поверх
// PostgreSQL: репозиторий версий пайплайнов, репозиторий запусков, unit-of-work с
// атомарной записью outbox и журнал публикации outbox.
//
// Адаптеры хранят агрегаты не указателями, а снимками (domain.PipelineSnapshot,
// domain.RunSnapshot) в колонке JSONB, а загрузка идёт через domain.Rehydrate*.
// Поэтому сохранённое состояние изолировано от вызывающего, а загрузка заново
// проверяет инварианты. Репозитории не открывают транзакцию сами, если привязаны к
// ней: NewTx возвращает их в рамках активной транзакции unit-of-work, где запись
// агрегата и запись его событий атомарны.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// agent-orchestration (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema).
const Schema = "agent-orchestration"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют и
// пул соединений, и активная транзакция, поэтому один и тот же код работает и вне,
// и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
