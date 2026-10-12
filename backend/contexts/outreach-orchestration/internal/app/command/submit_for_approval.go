package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ErrApprovalRejected — подача черновика на утверждение отвергнута по доменной
// причине: нарушена форма (пустой запуск, компания, ссылка черновика или материала,
// неположительная версия) или переданный черновик конфликтует с уже поставленным
// (та же ссылка черновика с другим составом). Отличается от недоступности: сбой
// хранилища или транзакции отказа команды не образует.
var ErrApprovalRejected = errors.New("приложение: подача черновика на утверждение отвергнута")

// SubmitForApproval — команда подачи черновика агента в очередь утверждения.
//
// Пара запуск+компания называет инициатора (RunID, CompanyID), но идентичность
// черновика несёт DraftRef: по нему читающий контракт решения (draftdecision) находит
// утверждённое действие, поэтому идемпотентность и скоуп кампании выводятся из
// (TenantID, DraftRef).
type SubmitForApproval struct {
	// TenantID — тенант-владелец кампании. Обязателен.
	TenantID domain.TenantID
	// RunID — запуск агента, подавший черновик. Обязателен; служит корреляцией.
	RunID string
	// MissionID — задание запуска. Может быть пустым.
	MissionID string
	// CompanyID — компания (лид) черновика. Обязательна; становится сегментом.
	CompanyID string
	// DraftRef — непрозрачная ссылка на черновик. Обязательна.
	DraftRef domain.DraftID
	// DraftVersion — версия материала у инициатора. Обязательна и положительна.
	DraftVersion int
	// MaterialRef — непрозрачная ссылка на материал черновика. Обязательна.
	MaterialRef domain.MaterialID
	// RecipientRef — непрозрачная ссылка на получателя. Пустая заменяется CompanyID.
	RecipientRef domain.RecipientID
}

// SubmitForApprovalResult — наблюдаемый исход подачи.
type SubmitForApprovalResult struct {
	// CampaignID — кампания, в которую поставлен черновик.
	CampaignID domain.CampaignID
	// DraftRef — ссылка поставленного черновика.
	DraftRef domain.DraftID
	// DraftVersion — версия черновика у владельца.
	DraftVersion int
	// Repeated — черновик с этой ссылкой уже стоял в очереди; второй не создавался.
	Repeated bool
}

// SubmitForApprovalHandler — use case подачи черновика агента на утверждение.
//
// Сценарий держит транзакцию, но не содержит доменных правил: черновик ставит
// агрегат campaign его методом DraftAction, а отправку запрещает до решения
// оператора (черновик остаётся «ожидает утверждения»). Отправка не выполняется.
type SubmitForApprovalHandler struct {
	clock ports.Clock
	uow   ports.UnitOfWork
}

// NewSubmitForApprovalHandler собирает use case из портов времени и транзакции.
func NewSubmitForApprovalHandler(clock ports.Clock, uow ports.UnitOfWork) *SubmitForApprovalHandler {
	return &SubmitForApprovalHandler{clock: clock, uow: uow}
}

// approvalCampaignWindow — горизонт окна кампании подачи. Команда не несёт
// временных границ, поэтому кампания открывается на срок, достаточный для решения
// оператора. Повтор не пересоздаёт кампанию, а находит уже созданную.
const approvalCampaignWindow = 30 * 24 * time.Hour

// Handle идемпотентно ставит черновик агента в очередь утверждения.
//
// Повтор с теми же (tenant, run, draft_ref) находит уже поставленный черновик:
// второй не создаётся. Тот же draft_ref с другим составом — доменный отказ
// ErrApprovalRejected: состояние не меняется. Некорректная форма — тоже отказ; сбой
// хранилища или транзакции пробрасывается как недоступность.
func (h *SubmitForApprovalHandler) Handle(ctx context.Context, cmd SubmitForApproval) (SubmitForApprovalResult, error) {
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID.String()))
	draftRef := domain.DraftID(strings.TrimSpace(cmd.DraftRef.String()))
	if err := validateSubmitForApproval(cmd, tenantID, draftRef); err != nil {
		return SubmitForApprovalResult{}, err
	}

	// Канал письма объявлен доменом; его разбор не может провалиться на константе.
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		return SubmitForApprovalResult{}, fmt.Errorf("%w: %v", ErrApprovalRejected, err)
	}

	campaignID := domain.DraftCampaignID(tenantID, draftRef)
	name := fmt.Sprintf("Черновик агента %s", strings.TrimSpace(cmd.CompanyID))
	spec := approvalSpec(cmd, channel)

	var result SubmitForApprovalResult
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := rejectIfCampaignCancelled(ctx, tx, tenantID.String(), string(campaignID)); err != nil {
			return err
		}
		campaign, repeated, err := loadOrCreateApprovalCampaign(
			ctx, tx.Campaigns(), campaignID, tenantID, name, h.clock.Now(),
		)
		if err != nil {
			return err
		}

		if existing, ok := campaign.Draft(draftRef); ok {
			// Черновик с этой ссылкой уже стоит в очереди. Тот же состав —
			// идемпотентный повтор; другой состав — конфликт, состояние не меняется.
			if existing.Spec() != spec {
				return fmt.Errorf("%w: черновик %s подан с другим составом", ErrApprovalRejected, draftRef)
			}
			result = SubmitForApprovalResult{
				CampaignID:   campaign.ID(),
				DraftRef:     draftRef,
				DraftVersion: existing.Version(),
				Repeated:     true,
			}
			return nil
		}

		draft, _, err := campaign.DraftAction(draftRef, spec, h.clock.Now())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrApprovalRejected, err)
		}

		result = SubmitForApprovalResult{
			CampaignID:   campaign.ID(),
			DraftRef:     draftRef,
			DraftVersion: draft.Version(),
			Repeated:     repeated,
		}

		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", campaignID, err)
		}
		if err := tx.Outbox().Append(ctx, campaign.DomainEvents()...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", campaignID, err)
		}
		campaign.PullDomainEvents()
		return nil
	})
	if err != nil {
		return SubmitForApprovalResult{}, err
	}
	return result, nil
}

// validateSubmitForApproval проверяет форму команды, называя все нарушения разом.
func validateSubmitForApproval(cmd SubmitForApproval, tenantID domain.TenantID, draftRef domain.DraftID) error {
	var invalid []string
	if tenantID == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(cmd.RunID) == "" {
		invalid = append(invalid, "run_id")
	}
	if strings.TrimSpace(cmd.CompanyID) == "" {
		invalid = append(invalid, "company_id")
	}
	if draftRef == "" {
		invalid = append(invalid, "draft_ref")
	}
	if cmd.DraftVersion <= 0 {
		invalid = append(invalid, "draft_version")
	}
	if strings.TrimSpace(cmd.MaterialRef.String()) == "" {
		invalid = append(invalid, "material_ref")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrApprovalRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// approvalSpec собирает состав черновика письма: канал email, материал и получатель
// из непрозрачных ссылок, сегментом служит компания. Пустая ссылка получателя
// заменяется ссылкой на компанию — минимальной непрозрачной ссылкой инициатора;
// адрес разрешает владелец доставки, персональные данные через границу не идут.
func approvalSpec(cmd SubmitForApproval, channel domain.ActionChannel) domain.DraftSpec {
	company := strings.TrimSpace(cmd.CompanyID)
	recipient := strings.TrimSpace(cmd.RecipientRef.String())
	if recipient == "" {
		recipient = company
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID(strings.TrimSpace(cmd.MaterialRef.String())),
		Segment:   domain.SegmentID(company),
		Recipient: domain.RecipientID(recipient),
		Channel:   channel,
	}
}

// loadOrCreateApprovalCampaign находит кампанию подачи или создаёт её. Найденная
// кампания чужого тенанта — отказ команды: границу тенанта команда не пересекает.
func loadOrCreateApprovalCampaign(
	ctx context.Context,
	repo ports.CampaignRepository,
	id domain.CampaignID,
	tenant domain.TenantID,
	name string,
	now time.Time,
) (*domain.Campaign, bool, error) {
	campaign, err := repo.FindByID(ctx, id)
	if err != nil && !isNotFound(err) {
		return nil, false, fmt.Errorf("загрузка кампании %s: %w", id, err)
	}
	if campaign != nil {
		if campaign.TenantID() != tenant {
			return nil, false, fmt.Errorf("%w: кампания %s принадлежит другому тенанту", ErrApprovalRejected, id)
		}
		return campaign, true, nil
	}

	window, err := domain.NewCampaignWindow(now, now.Add(approvalCampaignWindow))
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrApprovalRejected, err)
	}
	campaign, err = domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       id,
		TenantID: tenant,
		Name:     name,
		Window:   window,
	})
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrApprovalRejected, err)
	}
	return campaign, false, nil
}
