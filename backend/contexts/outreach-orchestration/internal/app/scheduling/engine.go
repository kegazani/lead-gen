// Package scheduling содержит движок последовательностей: проход по активным
// цепочкам, порождение черновиков шагов и продвижение по условиям перехода.
//
// Движок ничего не отправляет: он формирует черновик шага и ставит его в очередь
// утверждений (design.md, Решение 1). Неутверждённый шаг не пропускается и не
// утверждается по сроку — по нему фиксируется простой (design.md, Решение 2).
//
// Пакет зависит только от портов и домена: транзакцию задаёт unit-of-work,
// время — порт Clock, перечисление кампаний — порт CampaignLister.
package scheduling

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// StallNotice — уведомление о простое: у кампании есть участия с неутверждённым
// шагом. Движок не устраняет простой, а сообщает о нём.
type StallNotice struct {
	CampaignID domain.CampaignID
	Executions []domain.ExecutionID
}

// Report — итог одного прохода движка по всем кампаниям.
type Report struct {
	// Campaigns — число обработанных кампаний.
	Campaigns int

	// Drafted — число сформированных черновиков шагов.
	Drafted int

	// Advanced — число участий, продвинувшихся к следующему шагу.
	Advanced int

	// Completed — число завершённых участий.
	Completed int

	// Stalled — уведомления о простое по кампаниям с неутверждёнными шагами.
	Stalled []StallNotice
}

// Engine — движок последовательностей. Он читает кампании, продвигает их
// активные участия и сохраняет изменения вместе с событиями в одной транзакции.
type Engine struct {
	campaigns ports.CampaignLister
	uow       ports.UnitOfWork
	clock     ports.Clock
	limits    domain.ParticipationLimits
}

// NewEngine собирает движок. Ограничения участия и зависимости обязательны:
// движок не выдумывает политику.
func NewEngine(
	campaigns ports.CampaignLister,
	uow ports.UnitOfWork,
	clock ports.Clock,
	limits domain.ParticipationLimits,
) (*Engine, error) {
	if campaigns == nil {
		return nil, errors.New("scheduling: не задан источник кампаний")
	}
	if uow == nil {
		return nil, errors.New("scheduling: не задан unit-of-work")
	}
	if clock == nil {
		return nil, errors.New("scheduling: не задан источник времени")
	}
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &Engine{campaigns: campaigns, uow: uow, clock: clock, limits: limits}, nil
}

// RunOnce выполняет один проход по всем кампаниям. Ошибка одной кампании не
// останавливает проход по остальным: она накапливается, а проход продолжается,
// чтобы один сбой не блокировал остальные цепочки.
func (e *Engine) RunOnce(ctx context.Context) (Report, error) {
	ids, err := e.campaigns.ListCampaignIDs(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("scheduling: перечисление кампаний: %w", err)
	}

	var report Report
	var failures []error
	now := e.clock.Now()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		notice, err := e.advanceCampaign(ctx, id, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		report.Campaigns++
		report.Drafted += notice.drafted
		report.Advanced += notice.advanced
		report.Completed += notice.completed
		if len(notice.stalled) > 0 {
			report.Stalled = append(report.Stalled, StallNotice{CampaignID: id, Executions: notice.stalled})
		}
	}
	return report, errors.Join(failures...)
}

// campaignProgress — итог прохода по одной кампании.
type campaignProgress struct {
	drafted   int
	advanced  int
	completed int
	stalled   []domain.ExecutionID
}

// advanceCampaign выполняет проход по одной кампании в отдельной транзакции.
// Изменение сохраняется только тогда, когда проход породил событие: пустой
// проход не перезаписывает агрегат.
func (e *Engine) advanceCampaign(
	ctx context.Context,
	id domain.CampaignID,
	now time.Time,
) (campaignProgress, error) {
	var progress campaignProgress
	err := e.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		campaign, err := tx.Campaigns().FindByID(ctx, id)
		if errors.Is(err, ports.ErrCampaignNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scheduling: загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return nil
		}

		report, err := campaign.AdvanceSequences(now, e.limits)
		if err != nil {
			return fmt.Errorf("scheduling: проход кампании %s: %w", id, err)
		}
		progress.drafted = len(report.Drafted)
		progress.advanced = len(report.Advanced)
		progress.completed = len(report.Completed)
		progress.stalled = report.Stalled

		if len(campaign.DomainEvents()) == 0 {
			return nil
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("scheduling: сохранение кампании %s: %w", id, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("scheduling: запись событий кампании %s в outbox: %w", id, err)
		}
		campaign.PullDomainEvents()
		return nil
	})
	if err != nil {
		return campaignProgress{}, err
	}
	return progress, nil
}
