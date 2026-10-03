// Package postgres реализует порты контекста request-fulfillment поверх
// PostgreSQL: репозиторий заявок, unit-of-work с атомарной записью outbox,
// журнал публикации outbox и хранилище черновиков писем оператора.
//
// Адаптеры хранят агрегат не указателем, а снимком (domain.RequestSnapshot) в
// колонке JSONB, а загрузка идёт через domain.RehydrateRequest. Поэтому
// сохранённое состояние изолировано от вызывающего, а загрузка заново проверяет
// инварианты. Репозиторий заявок не открывает транзакцию сам, если привязан к
// ней: NewTx возвращает репозиторий в рамках активной транзакции unit-of-work,
// где запись агрегата и запись его событий атомарны.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// request-fulfillment (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "request-fulfillment"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
