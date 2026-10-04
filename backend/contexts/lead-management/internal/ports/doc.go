// Package ports — порты контекста lead-management: репозитории карточек,
// контактов и исходящих писем, хранилище заданий поиска и часы.
//
// Порты выражены интерфейсами и импортируют только домен и примитив jobs;
// адаптеры (pgx, in-memory) реализуют их за этим контрактом.
package ports

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// ErrProfileNotFound — карточка компании не найдена.
var ErrProfileNotFound = errors.New("lead-management: карточка компании не найдена")

// ErrMessageNotFound — исходящее сообщение не найдено.
var ErrMessageNotFound = errors.New("lead-management: сообщение не найдено")

// ErrContactNotFound — контакт не найден. Позволяет отличить доменный отказ
// (неизвестная ссылка) от сбоя чтения хранилища.
var ErrContactNotFound = errors.New("lead-management: контакт не найден")

// LeadProfileRepository — репозиторий карточек компаний (зелёная зона).
type LeadProfileRepository interface {
	// Save сохраняет карточку (создаёт или дополняет).
	Save(ctx context.Context, company *domain.Company) error
	// FindByID возвращает карточку по идентификатору.
	FindByID(ctx context.Context, id domain.CompanyID) (*domain.Company, error)
	// ListVisible возвращает не скрытые карточки (витрина).
	ListVisible(ctx context.Context, limit int) ([]*domain.Company, error)
	// ListSnoozed возвращает отложенных лидов (snooze_until != nil), чтобы
	// прикладной слой мог вернуть истёкших в активную воронку.
	ListSnoozed(ctx context.Context) ([]*domain.Company, error)
	// Delete удаляет карточку безвозвратно.
	Delete(ctx context.Context, id domain.CompanyID) error
}

// Clock — часы для детерминированных тестов и проставления дат.
type Clock interface {
	Now() time.Time
}
