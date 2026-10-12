package ports

import (
	"context"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// TenantSummary — строка read-модели списка тенантов для админ-консоли.
//
// Содержит платформенные атрибуты тенанта и идентифицирующие данные компании,
// нужные оператору для проверки заявки на регистрацию и согласования профиля:
// название, описание, ИНН, email компании, сайт, юридические данные, состояние,
// статус профиля и дата создания. Логины и контакты пользователей сюда не
// входят намеренно: экран проверяет компанию, а не её людей.
type TenantSummary struct {
	// ID — идентификатор тенанта (граница изоляции данных).
	ID domain.TenantID
	// CompanyName — название компании клиента.
	CompanyName string
	// State — состояние тенанта (active/disabled/pending/rejected).
	State domain.TenantState
	// Description — описание компании из заявки на регистрацию.
	Description string
	// INN — ИНН компании.
	INN string
	// CompanyEmail — email компании.
	CompanyEmail string
	// Website — сайт компании.
	Website string
	// LegalData — юридические данные компании.
	LegalData string
	// ProfileStatus — статус профиля компании.
	ProfileStatus domain.ProfileStatus
	// CreatedAt — время создания тенанта (подачи заявки).
	CreatedAt time.Time
}

// TenantDirectory — порт чтения списка тенантов для консоли владельца.
//
// Это read-модель, отдельная от порта репозитория агрегата: она отдаёт обзор
// тенантов, не восстанавливая агрегат и не раскрывая подчинённые сущности
// (пользователей, аудит). Реализацию подставляет точка сборки; адаптеры
// хранилища контекста реализуют и репозиторий, и этот порт.
type TenantDirectory interface {
	// List возвращает тенанты в устойчивом порядке, не зависящем от плана
	// запроса: список на экране должен быть воспроизводим. Ошибка означает
	// недоступность источника.
	List(ctx context.Context) ([]TenantSummary, error)
}
