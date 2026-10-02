package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

// ErrDealNotFound — сделка отсутствует во внешней CRM.
var ErrDealNotFound = errors.New("порты: сделка не найдена")

// ErrDealVersionConflict — сохранённая версия сделки не совпала с ожидаемой:
// кто-то изменил запись параллельно. Ошибка оптимистичной блокировки.
var ErrDealVersionConflict = errors.New("порты: конфликт версии сделки")

// ErrCRMUnavailable — внешняя CRM недоступна: адрес не сконфигурирован или
// транспортный адаптер не подключён. Потребитель не выдаёт это за отсутствие
// сделки и не выдумывает состояние.
var ErrCRMUnavailable = errors.New("порты: внешняя CRM недоступна")

// DealRepository — порт внешней CRM как системы-источника сделок (зона red,
// storage external, architecture/domain-catalog.yaml).
//
// Контекст не владеет хранилищем сделок: он читает и пишет их через границу
// внешней CRM. Реализация может быть сетевой (реальная CRM) или in-memory
// (стенд-ин среза); порт об этом не знает и оперирует только доменными типами.
type DealRepository interface {
	// Save сохраняет сделку целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, прочитанная при загрузке. Если сохранённая
	// версия ей не равна, изменение отвергается ошибкой, для которой истинно
	// errors.Is(err, ErrDealVersionConflict), и состояние не меняется. При
	// создании новой сделки expectedVersion равен нулю.
	Save(ctx context.Context, deal *domain.Deal, expectedVersion int) error

	// FindByID загружает сделку по идентификатору. Если сделки нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrDealNotFound); загруженный
	// агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.DealID) (*domain.Deal, error)

	// ListByStage возвращает сделки в заданном этапе, упорядоченные по
	// идентификатору. Обслуживает чтение выборок, а не доменное правило.
	ListByStage(ctx context.Context, stage domain.DealStage) ([]*domain.Deal, error)
}
