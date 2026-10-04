// Package postgres реализует порты контекста message-delivery поверх
// PostgreSQL: репозиторий сообщений, репозиторий репутации доменов, журнал
// transactional outbox и unit-of-work с атомарной записью агрегата и событий.
//
// Агрегат хранится снимком (domain.MessageSnapshot / domain.SenderReputationSnapshot),
// а загрузка идёт через domain.Rehydrate*: сохранённое состояние изолировано от
// вызывающего, загрузка заново проверяет инварианты. Репозитории не открывают
// транзакцию сами, если привязаны к ней: NewTx возвращает адаптер в рамках
// активной транзакции unit-of-work, где запись агрегата и запись его событий
// атомарны.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// message-delivery (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "message-delivery"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
