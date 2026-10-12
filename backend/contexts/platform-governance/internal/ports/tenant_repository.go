package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// ErrTenantNotFound — тенант отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища.
var ErrTenantNotFound = errors.New("порты: тенант не найден")

// TenantRepository — порт сохранения и загрузки aggregate root tenant.
//
// Репозиторий существует только для aggregate root: пользователи входят в
// состав тенанта и записываются вместе с ним. Портовый метод не знает о драйвере
// БД и схеме таблиц.
type TenantRepository interface {
	// Save сохраняет агрегат целиком. Повторное сохранение тенанта заменяет
	// прежнее состояние (upsert по идентификатору).
	Save(ctx context.Context, tenant *domain.Tenant) error

	// FindByID загружает тенант по идентификатору. Если тенанта нет,
	// возвращается ошибка, для которой истинно errors.Is(err, ErrTenantNotFound);
	// загруженный агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error)

	// FindByIdentity ищет тенант по идентифицирующим данным компании — ИНН или
	// email компании. Пустые значения не совпадают. Если тенанта нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrTenantNotFound).
	FindByIdentity(ctx context.Context, inn, companyEmail string) (*domain.Tenant, error)

	// FindUserByLogin ищет пользователя по логину (email учётной записи) среди
	// всех тенантов и возвращает тенант-владельца и идентификатор пользователя.
	// Пустой логин не совпадает. Если пользователя нет, возвращается ошибка, для
	// которой истинно errors.Is(err, ErrTenantNotFound).
	FindUserByLogin(ctx context.Context, login string) (*domain.Tenant, domain.UserID, error)
}
