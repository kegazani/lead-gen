package ports

import (
	"context"
	"errors"
)

// ErrDraftStoreUnavailable — хранилище черновиков писем не подключено. Это
// отдельный исход чтения, а не «черновиков нет»: последнее выражается пустым
// списком.
var ErrDraftStoreUnavailable = errors.New("черновики писем: хранилище недоступно")

// EmailDraft — черновик письма под найденный лид: идентификаторы, шапка и тело
// письма. Черновик живёт в контексте заявки и адресован оператору; в payload
// workflow текст письма не поднимается (payload-гигиена).
type EmailDraft struct {
	// RequestID — заявка, под которую сгенерировано письмо.
	RequestID string
	// TenantID — тенант-владелец черновика.
	TenantID string
	// LeadID — идентификатор лида (нормализованный домен компании).
	LeadID string
	// Subject — тема письма.
	Subject string
	// Body — тело письма.
	Body string
	// Language — язык письма. Может быть пустым.
	Language string
	// RecipientRef — непрозрачная ссылка на получателя письма: идентификатор
	// контакта лида с пригодным адресом, а не сам адрес. Пусто — пригодного
	// адреса нет, письмо в передачу не входит. Персональных данных не несёт.
	RecipientRef string
}

// EmailDraftStore — порт хранения черновиков писем по заявке. Отдельный от
// репозитория заявок: черновик — материал оператора, а не часть aggregate root.
type EmailDraftStore interface {
	// SaveDrafts сохраняет черновики, добавляя их к уже имеющимся. Повторный
	// черновик по тому же лиду заявки заменяет прежний: шаг идемпотентен и
	// переживает повторный запуск activity.
	SaveDrafts(ctx context.Context, drafts []EmailDraft) error

	// ListByRequest возвращает черновики заявки в порядке найденных лидов. Чужие
	// тенанты не возвращаются: изоляция обеспечивается фильтром владельца.
	ListByRequest(ctx context.Context, requestID, tenantID string) ([]EmailDraft, error)
}
