// Package postgres — заготовка PostgreSQL-адаптера CampaignRepository.
//
// ЗАДАЧА 5.2 ЗАБЛОКИРОВАНА РЕШЕНИЕМ ПО ЗАВИСИМОСТИ.
//
// Реализация PostgreSQL-адаптера требует драйвера PostgreSQL (pgx) и сетевого
// доступа к базе, доступной в окружении. Ни одного из этих условий сейчас нет:
//   - внешние Go-зависимости добавлять запрещено, пока не принято решение по
//     зависимости, поэтому pgx/lib/pq в go.mod не вводятся;
//   - в стандартной библиотеке Go драйвера PostgreSQL нет;
//   - сеть недоступна, поэтому contract suite против реальной базы не гоняется.
//
// Пакет намеренно оставлен незавершённым: вместо нерабочей «почти реализации»
// здесь объявлен только интерфейсный каркас, который компилируется без внешних
// зависимостей и честно сообщает о блокировке. Доменные типы и порт
// подделывать незачем: как только появится решение по зависимости, сюда
// добавляются Save через UPSERT агрегата и FindByID через сборку
// domain.CampaignSnapshot и domain.RehydrateCampaign, и этот же пакет
// подключается к contracttest.RunCampaignRepositoryContract.
package postgres

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ErrDriverUnavailable сообщает, что адаптер не подключён к базе: отсутствует
// драйвер PostgreSQL и/или решение по внешней зависимости (задача 5.2).
var ErrDriverUnavailable = errors.New(
	"adapters/postgres: PostgreSQL-драйвер не подключён: задача 5.2 заблокирована решением по зависимости (нужен pgx и сетевой доступ)",
)

// Repository — компилируемый каркас ports.CampaignRepository без драйвера.
//
// Тип не является рабочей реализацией: оба метода возвращают
// ErrDriverUnavailable. Он существует, чтобы зафиксировать форму адаптера и
// гарантировать, что порт не «протекает» типами драйвера, и чтобы подключение
// реальной реализации не меняло сигнатуры порта.
type Repository struct{}

// Repository удовлетворяет порту репозитория кампаний.
var _ ports.CampaignRepository = (*Repository)(nil)

// New возвращает каркас адаптера. Реальное подключение к базе появится вместе
// с решением по зависимости.
func New() *Repository { return &Repository{} }

// Save не реализована.
func (*Repository) Save(context.Context, *domain.Campaign) error {
	return ErrDriverUnavailable
}

// FindByID не реализована.
func (*Repository) FindByID(context.Context, domain.CampaignID) (*domain.Campaign, error) {
	return nil, ErrDriverUnavailable
}
