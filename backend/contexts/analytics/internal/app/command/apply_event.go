package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
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

	// Измерения отчётности: источник, отрасль, сегмент, кампания и менеджер.
	// Необязательны; уточняют срез витрины, но не несут персональных данных.
	Source     string
	Industry   string
	Segment    string
	CampaignID string
	ManagerID  string

	// Поля исхода классификации (kind = lead-assessment): задание, вердикт и
	// категория причины. Необязательны для прочих видов событий; питают проекцию
	// качества поиска, а не дашборд клиента.
	JobID   string
	Verdict string
	Reason  string
}

// ApplyEventHandler — use case применения события к read-модели.
type ApplyEventHandler struct {
	model    *readmodel.ReadModel
	store    ports.ProjectionStore
	outcomes *ApplyOutcomeHandler
}

// NewApplyEventHandler собирает use case поверх проекции. store необязателен:
// без хранилища проекция остаётся в памяти процесса (dev и тесты); с хранилищем
// состояние компании записывается на носитель и переживает рестарт. outcomes
// необязателен: без него события классификации ведут к честной недоступности
// проекции качества поиска.
func NewApplyEventHandler(model *readmodel.ReadModel, store ports.ProjectionStore, outcomes *ApplyOutcomeHandler) *ApplyEventHandler {
	return &ApplyEventHandler{model: model, store: store, outcomes: outcomes}
}

// Handle нормализует команду в событие проекции, применяет его и, если задано
// хранилище, сохраняет получившийся снимок компании. Событие без тенанта или
// компании проекция игнорирует сама: прикладной слой не выдумывает
// принадлежность и ничего не сохраняет по такому событию.
//
// Ошибка означает, что снимок не записан на носитель: вызывающий решает, повторить
// применение (доставка at-least-once) или отклонить. Само применение к проекции
// идемпотентно по снимку, поэтому повтор не искажает счётчики.
func (h *ApplyEventHandler) Handle(ctx context.Context, cmd ApplyEvent) error {
	// Событие классификации питает отдельную проекцию качества поиска: вердикты и
	// причины не искажают воронку клиентского дашборда и учитываются раздельно.
	if readmodel.Kind(cmd.Kind) == readmodel.KindLeadAssessment {
		if h.outcomes == nil {
			return ErrInsightUnavailable
		}
		return h.outcomes.Handle(ctx, ApplyOutcome{
			TenantID:   cmd.TenantID,
			JobID:      cmd.JobID,
			CompanyID:  cmd.CompanyID,
			Source:     cmd.Source,
			Industry:   cmd.Industry,
			Verdict:    cmd.Verdict,
			Reason:     cmd.Reason,
			OccurredAt: cmd.OccurredAt,
		})
	}

	event := readmodel.Event{
		TenantID:   cmd.TenantID,
		Kind:       readmodel.Kind(cmd.Kind),
		CompanyID:  cmd.CompanyID,
		Status:     cmd.Status,
		Outcome:    cmd.Outcome,
		OccurredAt: cmd.OccurredAt,
		ReplyText:  cmd.ReplyText,

		Source:     cmd.Source,
		Industry:   cmd.Industry,
		Segment:    cmd.Segment,
		CampaignID: cmd.CampaignID,
		ManagerID:  cmd.ManagerID,
	}
	h.model.Apply(event)

	if h.store == nil {
		return nil
	}
	record, ok := h.model.Record(event.TenantID, event.CompanyID)
	if !ok {
		// Событие отброшено проекцией (пустой тенант или компания) — сохранять
		// нечего.
		return nil
	}
	if err := h.store.SaveCompany(ctx, record); err != nil {
		return fmt.Errorf("command: сохранение состояния компании: %w", err)
	}
	return nil
}
