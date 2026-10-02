package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
)

// Ошибки портов контекста engagement-observation. Ошибка описывает исход
// обращения к хранилищу или журналу, а не нарушение правила домена.
var (
	// ErrEventNotFound — событие взаимодействия не найдено либо принадлежит
	// другому тенанту. Существование чужого события этим отказом не раскрывается.
	ErrEventNotFound = errors.New("порты: событие взаимодействия не найдено")

	// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой.
	ErrVersionConflict = errors.New("порты: конфликт версии события взаимодействия")

	// ErrLinkNotFound — трекинг-ссылка неизвестна владельцу.
	ErrLinkNotFound = errors.New("порты: трекинг-ссылка не найдена")
)

// EngagementRepository — хранилище агрегатов engagement-event.
type EngagementRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой по версии.
	// При создании (агрегата ещё нет) expectedVersion равен нулю.
	Save(ctx context.Context, event *domain.EngagementEvent, expectedVersion int) error

	// FindByID загружает агрегат по идентификатору. Отсутствие даёт ошибку, для
	// которой истинно errors.Is(err, ErrEventNotFound).
	FindByID(ctx context.Context, id domain.EngagementEventID) (*domain.EngagementEvent, error)

	// ListByTenant возвращает события тенанта, упорядоченные по идентификатору.
	ListByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.EngagementEvent, error)
}

// SignalLedger — журнал обработанных сырых сигналов. Инвариант владельца:
// входящий webhook сначала дедуплицируется, поэтому дедупликация выполняется до
// квалификации и в той же транзакции, что и запись события.
type SignalLedger interface {
	// Seen сообщает, обрабатывался ли сигнал ранее.
	Seen(ctx context.Context, key domain.SignalID) (bool, error)

	// Mark фиксирует сигнал как обработанный.
	Mark(ctx context.Context, key domain.SignalID) error
}

// TransitionLinks — реестр трекинг-ссылок для capability read-tracking. По
// непрозрачной ссылке владелец отдаёт адрес назначения перехода.
type TransitionLinks interface {
	// Save регистрирует ссылку перехода.
	Save(ctx context.Context, link domain.TransitionLink) error

	// Resolve возвращает ссылку перехода по непрозрачному значению. Отсутствие
	// даёт ошибку, для которой истинно errors.Is(err, ErrLinkNotFound).
	Resolve(ctx context.Context, ref domain.LinkRef) (domain.TransitionLink, error)
}
