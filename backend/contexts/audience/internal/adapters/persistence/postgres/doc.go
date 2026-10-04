// Package postgres реализует порты хранения контекста audience поверх
// PostgreSQL: репозиторий сегментов (оптимистичная блокировка), репозиторий
// снимков аудитории (upsert по идентификатору), unit-of-work с атомарной записью
// outbox и журнал публикации outbox.
//
// Адаптеры хранят агрегат не указателем, а снимком (domain.SegmentSnapshot,
// domain.AudienceSnapshotState) в колонке JSONB, а загрузка идёт через
// domain.RehydrateSegment и domain.RehydrateAudienceSnapshot. Поэтому сохранённое
// состояние изолировано от вызывающего, а загрузка заново проверяет инварианты.
//
// Снимок агрегата нельзя положить в JSONB напрямую: value objects домена
// (segment-criteria, score-value, signal, snapshot-criteria) непрозрачны — их поля
// не экспортируются, поэтому encoding/json не переносит их содержимое. Адаптер
// переводит снимок в записываемую форму (records.go): она раскладывает value
// objects на примитивы через экспортированные аксессоры, а при чтении собирает
// обратно через экспортированные конструкторы домена. Так граница сериализации не
// проникает в домен.
//
// Репозитории не открывают транзакцию сами, если привязаны к ней: NewTx
// возвращает репозиторий в рамках активной транзакции unit-of-work, где запись
// агрегата и запись его событий атомарны.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме audience
// (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "audience"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
