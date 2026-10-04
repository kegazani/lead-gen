// Package postgres реализует порты хранилища контекста engagement-observation
// поверх PostgreSQL: хранилище событий взаимодействия (EngagementRepository),
// журнал дедупликации сигналов (SignalLedger), реестр трекинг-ссылок
// (TransitionLinks), unit-of-work с атомарной записью outbox и журнал публикации
// outbox платформы.
//
// Адаптеры хранят агрегат не указателем, а снимком (domain.EngagementSnapshot), а
// загрузка идёт через domain.Rehydrate. Поэтому сохранённое состояние изолировано
// от вызывающего, а загрузка заново проверяет инварианты. Репозиторий не
// открывает транзакцию сам, если привязан к ней: NewTx возвращает репозиторий в
// рамках активной транзакции unit-of-work, где запись события, отметка
// дедупликации, регистрация ссылки и запись событий outbox атомарны.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// engagement-observation (Schema) и не обращаются к схемам других сервисов.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "engagement-observation"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

// rowScanner — общая поверхность pgx.Row и pgx.Rows, достаточная для Scan одной
// строки снимка.
type rowScanner interface {
	Scan(dest ...any) error
}
