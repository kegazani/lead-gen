// Package postgres реализует порты хранения контекста integration-platform
// поверх PostgreSQL: репозиторий регистраций адаптеров и репозиторий вызовов
// интеграций.
//
// Адаптеры хранят агрегат не указателем, а снимком
// (domain.AdapterRegistrationSnapshot, domain.IntegrationInvocationSnapshot):
// Save пишет снимок, Find* читает его и восстанавливает агрегат через
// domain.RehydrateAdapterRegistration / domain.RehydrateIntegrationInvocation.
// Поэтому сохранённое состояние изолировано от вызывающего, а загрузка заново
// проверяет инварианты. Реализация не знает о бизнес-правилах: «нет сущности» —
// свойство хранилища (ports.ErrAdapterRegistrationNotFound,
// ports.ErrInvocationNotFound), а противоречивый снимок отвергает домен.
//
// Граница данных — schema-per-service: адаптеры работают только в схеме
// integration-platform (Schema) и не обращаются к схемам других сервисов.
//
// # Транзакция
//
// Запись регистрации составная (заголовок и объявленные возможности), поэтому
// адаптер регистраций открывает собственную транзакцию на Save, если не привязан
// к уже активной. Вызов пишется одной строкой, транзакции не требует.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "integration-platform"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}
