// Package postgres реализует порты хранилища контекста content-production поверх
// PostgreSQL: репозитории контент-пакетов и заданий рендеринга, unit-of-work с
// атомарной записью outbox и журнал публикации outbox.
//
// Адаптеры хранят агрегат не указателем, а снимком состояния
// (domain.ContentPackageSnapshot, domain.RenderJobSnapshot) в колонке JSONB, а
// загрузка идёт через Rehydrate-функции домена. Поэтому сохранённое состояние
// изолировано от вызывающего, а загрузка заново проверяет инварианты. Снимок
// пишется целиком (upsert по идентификатору), а денормализованные колонки версии и
// состояния обслуживают оптимистичную блокировку и выборку очереди рендеринга.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// content-production (Schema) и не обращаются к схемам других сервисов.
//
// Репозитории не открывают транзакцию сами, если привязаны к ней: конструкторы
// NewContentTx и NewRenderJobTx возвращают репозиторий в рамках активной
// транзакции unit-of-work, где запись агрегата и запись его событий атомарны.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "content-production"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
