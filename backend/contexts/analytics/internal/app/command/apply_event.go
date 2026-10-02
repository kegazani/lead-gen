package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// ApplyEvent — команда применения нормализованного доменного события к проекции.
//
// ReplyText принимается на входе, но в проекцию не переносится: событие ответа
// учитывается только счётчиком. Поле существует, чтобы маскирование было
// проверяемо, а не декларативно.
type ApplyEvent struct {
	TenantID   string
	Kind       string
	CompanyID  string
	Status     string
	Outcome    string
	OccurredAt time.Time
	ReplyText  string
}

// ApplyEventHandler — use case применения события к read-модели.
type ApplyEventHandler struct {
	model *readmodel.ReadModel
}

// NewApplyEventHandler собирает use case поверх проекции.
func NewApplyEventHandler(model *readmodel.ReadModel) *ApplyEventHandler {
	return &ApplyEventHandler{model: model}
}

// Handle нормализует команду в событие проекции и применяет его. Событие без
// тенанта или компании проекция игнорирует сама: прикладной слой не выдумывает
// принадлежность.
func (h *ApplyEventHandler) Handle(_ context.Context, cmd ApplyEvent) {
	h.model.Apply(readmodel.Event{
		TenantID:   cmd.TenantID,
		Kind:       readmodel.Kind(cmd.Kind),
		CompanyID:  cmd.CompanyID,
		Status:     cmd.Status,
		Outcome:    cmd.Outcome,
		OccurredAt: cmd.OccurredAt,
		ReplyText:  cmd.ReplyText,
	})
}
