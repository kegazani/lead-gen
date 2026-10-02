package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// ChangeLeadStage — use case переноса лида между стадиями воронки. Переход
// фиксируется в операционной истории (время, автор) и сохраняется на лиде.
type ChangeLeadStage struct {
	Profiles ports.LeadProfileRepository
	Activity ports.LeadActivityStore
	Clock    ports.Clock

	// Events — издатель нормализованных событий дашборда (lead-workflow). nil —
	// публикация не собрана. TenantID — тенант, в срезе которого учитывается
	// событие.
	Events   ports.AnalyticsPublisher
	TenantID string
	Logger   observ.Logger
}

// Change переносит лида на стадию stage, фиксируя переход с автором. Стадия вне
// согласованного набора отклоняется доменом.
func (u *ChangeLeadStage) Change(ctx context.Context, leadID string, stage domain.Stage, author string) error {
	company, err := u.Profiles.FindByID(ctx, domain.CompanyID(leadID))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return ErrCardNotFound
	}
	if err != nil {
		return err
	}
	at := u.now()
	if err := company.ChangeStage(stage, at); err != nil {
		return err
	}
	if err := u.Profiles.Save(ctx, company); err != nil {
		return err
	}
	if err := u.Activity.RecordStageChange(ctx, company.ID(), stage, author, at); err != nil {
		return err
	}
	u.publishStage(ctx, company.ID().String(), stage, at)
	return nil
}

// publishStage публикует событие lead-workflow дашборда. Публикация — вторичный
// эффект: переход уже зафиксирован, и её сбой не отменяет смену стадии. Ошибка
// фиксируется логом, а не возвращается вызывающему.
func (u *ChangeLeadStage) publishStage(ctx context.Context, companyID string, stage domain.Stage, at time.Time) {
	if u.Events == nil {
		return
	}
	event := ports.AnalyticsEvent{
		EventID:    fmt.Sprintf("lead-workflow:%s:%s:%d", companyID, stage, at.UnixNano()),
		TenantID:   u.TenantID,
		Kind:       ports.AnalyticsKindLeadWorkflow,
		CompanyID:  companyID,
		Status:     stage.String(),
		OccurredAt: at,
	}
	if err := u.Events.Publish(ctx, event); err != nil && u.Logger != nil {
		u.Logger.Warn("workflow: публикация события дашборда не удалась", observ.Fields{}, "event_id", event.EventID, "error", err.Error())
	}
}

// SnoozeLead — use case отложенного возврата лида в пул: snooze до срока и
// возврат истёкших лидов в активную воронку.
type SnoozeLead struct {
	Profiles ports.LeadProfileRepository
	Clock    ports.Clock
}

// Snooze откладывает лида до срока until: до него лид скрыт из активной воронки.
func (u *SnoozeLead) Snooze(ctx context.Context, leadID string, until time.Time) error {
	company, err := u.Profiles.FindByID(ctx, domain.CompanyID(leadID))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return ErrCardNotFound
	}
	if err != nil {
		return err
	}
	company.Snooze(until, u.now())
	return u.Profiles.Save(ctx, company)
}

// ReturnExpired возвращает в активную воронку лидов, чей срок snooze истёк, и
// возвращает число возвращённых. Операция идемпотентна: лид со сроком в будущем
// не трогается.
func (u *SnoozeLead) ReturnExpired(ctx context.Context) (int, error) {
	now := u.now()
	snoozed, err := u.Profiles.ListSnoozed(ctx)
	if err != nil {
		return 0, err
	}
	returned := 0
	for _, company := range snoozed {
		if company.Status().InPool(now) {
			company.ReturnToPool(now)
			if err := u.Profiles.Save(ctx, company); err != nil {
				return returned, err
			}
			returned++
		}
	}
	return returned, nil
}

// now возвращает текущий момент часов либо системное время.
func (u *ChangeLeadStage) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// now возвращает текущий момент часов либо системное время.
func (u *SnoozeLead) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}
