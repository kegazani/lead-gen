package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/providerport"
)

// ProviderPort — порт вызова внешней интеграции. Реализуется адаптером
// поставщика; прикладной слой видит только этот интерфейс и не импортирует SDK
// (design.md, Решение 3). Контракт совпадает с опубликованным provider-port:
// приложение и поставщик связаны формой контракта, а не реализацией.
type ProviderPort = providerport.Port

// ErrAdapterRegistrationNotFound — регистрация адаптера отсутствует в хранилище.
// Ошибка порта, а не домена: «не найдено» — свойство хранилища.
var ErrAdapterRegistrationNotFound = errors.New("порты: регистрация адаптера не найдена")

// ErrInvocationNotFound — вызов интеграции отсутствует в хранилище.
var ErrInvocationNotFound = errors.New("порты: вызов интеграции не найден")

// AdapterRegistrationRepository — порт сохранения и загрузки aggregate root
// adapter-registration. Порт оперирует только доменными типами и не знает о
// драйвере БД, схеме таблиц и SQL.
type AdapterRegistrationRepository interface {
	// Save сохраняет регистрацию целиком. Повторный вызов для того же
	// идентификатора заменяет прежнее состояние (upsert по идентификатору).
	Save(ctx context.Context, registration *domain.AdapterRegistration) error

	// FindByID загружает регистрацию по идентификатору. Отсутствие даёт ошибку,
	// для которой errors.Is(err, ErrAdapterRegistrationNotFound) истинна.
	FindByID(ctx context.Context, id domain.AdapterID) (*domain.AdapterRegistration, error)

	// FindByName загружает регистрацию по стабильному имени адаптера: имя
	// уникально, повторная регистрация того же имени отвергается use case.
	FindByName(ctx context.Context, name domain.AdapterName) (*domain.AdapterRegistration, error)

	// List возвращает все регистрации для политики adapter-selection. Порядок
	// детерминирован, чтобы выбор был воспроизводим.
	List(ctx context.Context) ([]*domain.AdapterRegistration, error)
}

// IntegrationInvocationRepository — порт сохранения и загрузки aggregate root
// integration-invocation.
type IntegrationInvocationRepository interface {
	// Save сохраняет вызов целиком (upsert по идентификатору).
	Save(ctx context.Context, invocation *domain.IntegrationInvocation) error

	// FindByID загружает вызов по идентификатору. Отсутствие даёт ошибку, для
	// которой errors.Is(err, ErrInvocationNotFound) истинна.
	FindByID(ctx context.Context, id domain.InvocationID) (*domain.IntegrationInvocation, error)

	// FindByIdempotencyKey загружает вызов по ключу идемпотентности. Отсутствие
	// даёт ошибку, для которой errors.Is(err, ErrInvocationNotFound) истинна.
	FindByIdempotencyKey(ctx context.Context, key string) (*domain.IntegrationInvocation, error)

	// FindLatestByCorrelation загружает последний по времени вызов в указанном
	// correlation-ref. Нужен политике восстановления: успех после временной
	// ошибки в том же correlation-ref даёт integration-recovered.
	FindLatestByCorrelation(ctx context.Context, ref domain.CorrelationRef) (*domain.IntegrationInvocation, error)
}
