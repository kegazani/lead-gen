// Package postgres реализует порты контекста lead-management поверх PostgreSQL:
// репозиторий карточек компаний, репозиторий контактов, репозиторий исходящих
// писем, репозиторий шаблонов письма и хранилище заданий поиска.
//
// Адаптеры хранят агрегаты не указателями, а снимками (domain.CompanySnapshot,
// domain.ContactSnapshot) в колонке JSONB, а загрузка идёт через
// domain.RehydrateCompany и domain.RehydrateContact. Поэтому сохранённое
// состояние изолировано от вызывающего, а загрузка заново проверяет инварианты.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// lead-management (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "lead-management"

// execer — минимальная поверхность драйвера, нужная репозиториям. Ей
// удовлетворяют и пул соединений, и активная транзакция, поэтому один и тот же
// код работает и вне, и внутри транзакции.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
