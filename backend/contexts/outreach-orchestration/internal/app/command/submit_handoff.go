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

// ErrHandoffRejected — команда передачи отвергнута по доменной причине: нарушена
// форма (пустая заявка/тенант/ссылка письма) или переданный черновик конфликтует
// с уже созданным состоянием (тот же лид с другим составом). Отличается от
// недоступности: сбой хранилища или решения о доступе отказ команды не образует.
var ErrHandoffRejected = errors.New("приложение: команда передачи отвергнута")

// SubmitHandoffItem — одно письмо передачи: непрозрачные ссылки на лида,
// получателя и материал. Персональных данных не несёт: адрес и имя разрешает
// владелец в момент доставки.
type SubmitHandoffItem struct {
	// LeadID — лид, к которому относится письмо. Он становится сегментом черновика
	// (письмо адресовано конкретному лиду).
	LeadID string
	// RecipientRef — непрозрачная ссылка на получателя (контакт с пригодным
	// адресом). Из неё выводится стабильный идентификатор черновика: relay переносит
	// draft_id в RecipientRef утверждённого действия, поэтому черновик адресует
	// получателя именно ей.
	RecipientRef string
	// MaterialRef — непрозрачная ссылка на материал письма (`asset@version`).
	MaterialRef string
}

// SubmitHandoff — команда передачи писем заявки. RequestID — ключ идемпотентности:
// повтор с тем же request_id не создаёт вторую кампанию и не дублирует черновики.
type SubmitHandoff struct {
	RequestID        string
	TenantID         string
	Actor            string
	Name             string
	ExecutionPurpose string
	Items            []SubmitHandoffItem
}

// SubmitHandoffResult — наблюдаемый исход передачи.
type SubmitHandoffResult struct {
	// CampaignID — идентификатор кампании заявки. Пусто, если состав пуст и
	// кампания не создавалась.
	CampaignID domain.CampaignID
	// Accepted — число черновиков, поставленных в очередь этим вызовом.
	Accepted int
	// Skipped — число писем, уже стоявших в очереди (идемпотентный повтор): они не
	// дублируются. Сумма Accepted и Skipped равна числу переданных писем, поэтому
	// скрытая потеря письма без наблюдаемого признака невозможна.
	Skipped int
	// Repeated — кампания заявки уже существовала до вызова.
	Repeated bool
}

// SubmitHandoffHandler — use case приёма передачи писем заявки.
//
// Сценарий держит транзакцию и проверку права действовать в тенанте, но не
// содержит доменных правил: кампанию создаёт агрегат campaign, состав черновиков
// определяет его метод DraftAction. Отправка не выполняется: черновики остаются в
// состоянии «ожидает утверждения». Создание кампании и всех черновиков идёт в
// одной транзакции, поэтому частично созданное состояние при сбое не фиксируется.
type SubmitHandoffHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
}

// NewSubmitHandoffHandler собирает use case из портов доступа, времени и
// транзакции.
func NewSubmitHandoffHandler(access ports.AccessDecision, clock ports.Clock, uow ports.UnitOfWork) *SubmitHandoffHandler {
	return &SubmitHandoffHandler{access: access, clock: clock, uow: uow}
}

// handoffCampaignWindow — горизонт окна кампании передачи. Передача не несёт
// временных границ, поэтому кампания заявки открывается на срок, достаточный для
// утверждения оператором. Границы не влияют на идемпотентность: повтор не
// пересоздаёт кампанию, а находит уже созданную.
const handoffCampaignWindow = 30 * 24 * time.Hour

// Handle принимает команду передачи и идемпотентно создаёт одну кампанию на
// заявку с черновиками в очереди утверждения.
//
// Пустой состав кампанию не создаёт. Повтор по request_id находит существующую
// кампанию: новая не создаётся, дубли черновиков не заводятся, уже созданные
// черновики сохраняются. Некорректная форма даёт доменный отказ ErrHandoffRejected,
// сбой хранилища или транзакции — недоступность.
func (h *SubmitHandoffHandler) Handle(ctx context.Context, cmd SubmitHandoff) (SubmitHandoffResult, error) {
	// Editorial вход обрабатывает только отдельный editorial store/порт. Если
	// вызвать delivery use case напрямую с этим purpose, команда fail-closed.
	if cmd.ExecutionPurpose != "" && cmd.ExecutionPurpose != "delivery" {
		return SubmitHandoffResult{}, fmt.Errorf("%w: purpose %q не допускается в delivery handoff", ErrHandoffRejected, cmd.ExecutionPurpose)
	}
	requestID := strings.TrimSpace(cmd.RequestID)
	tenantID := domain.TenantID(strings.TrimSpace(cmd.TenantID))
	if requestID == "" || tenantID == "" {
		return SubmitHandoffResult{}, fmt.Errorf("%w: обязательны request_id и tenant_id", ErrHandoffRejected)
	}
	if err := validateHandoffItems(cmd.Items); err != nil {
		return SubmitHandoffResult{}, err
	}

	// Пустой состав: кампанию и черновики не создаём, исход сообщаем честно.
	// Фиктивный черновик не подставляется.
	if len(cmd.Items) == 0 {
		return SubmitHandoffResult{}, nil
	}

	// Передача — команда межсервисной границы. Оператора в ней может не быть
	// (контракт допускает пустой Actor): тогда проверять право оператора не на чем,
	// и границу тенанта держит сам агрегат. Если инициатор назван, право
	// проверяется как обычно.
	if actor := domain.ActorID(strings.TrimSpace(cmd.Actor)); actor != "" {
		if err := authorize(ctx, h.access, actor, tenantID); err != nil {
			return SubmitHandoffResult{}, err
		}
	}

	// Канал письма объявлен доменом; его разбор не может провалиться на константе
	// домена, но ошибка не маскируется.
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		return SubmitHandoffResult{}, fmt.Errorf("%w: %v", ErrHandoffRejected, err)
	}

	campaignID := handoffCampaignID(requestID)
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		name = fmt.Sprintf("Передача заявки %s", requestID)
	}

	var result SubmitHandoffResult
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		transactionFence, ok := tx.(ports.TransactionRequestCancellationFence)
		if !ok || transactionFence.RequestCancellationFence() == nil {
			return errors.New("приём передачи: адаптер не поддерживает durable cancellation fence")
		}
		cancelled, err := transactionFence.RequestCancellationFence().RegisterHandoff(ctx, tenantID.String(), requestID, string(campaignID))
		if err != nil {
			return err
		}
		if cancelled {
			return fmt.Errorf("%w: заявка %s отменена", ErrHandoffRejected, requestID)
		}
		campaign, repeated, err := loadOrCreateHandoffCampaign(ctx, tx.Campaigns(), campaignID, tenantID, name, h.clock.Now())
		if err != nil {
			return err
		}

		accepted, skipped := 0, 0
		for _, item := range cmd.Items {
			// draft_id — ссылка получателя: relay переносит draft_id в RecipientRef
			// утверждённого действия, а мост доставки разрешает её в адрес. Идентификатор
			// стабилен для идемпотентности: повтор не дублирует черновик. Сегментом
			// служит лид (spec.Segment), поэтому связь с найденной компанией сохраняется.
			draftID := domain.DraftID(strings.TrimSpace(item.RecipientRef))
			spec := handoffSpec(item, channel)

			if existing, ok := campaign.Draft(draftID); ok {
				// Черновик с этим лидом уже стоит в очереди. Тот же состав —
				// идемпотентный повтор: не дублируем. Другой состав — конфликт с
				// существующим состоянием: отказ команды, состояние не меняется.
				if existing.Spec() != spec {
					return fmt.Errorf("%w: черновик %s передан с другим составом", ErrHandoffRejected, draftID)
				}
				skipped++
				continue
			}

			if _, _, err := campaign.DraftAction(draftID, spec, h.clock.Now()); err != nil {
				return fmt.Errorf("%w: %v", ErrHandoffRejected, err)
			}
			accepted++
		}

		result = SubmitHandoffResult{
			CampaignID: campaign.ID(),
			Accepted:   accepted,
			Skipped:    skipped,
			Repeated:   repeated,
		}

		// Новых черновиков нет — состояние не менялось, транзакция не пишет.
		if accepted == 0 {
			return nil
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
		return SubmitHandoffResult{}, err
	}
	return result, nil
}

// handoffCampaignID выводит идентификатор кампании из заявки детерминированно:
// повтор той же заявки адресует ту же кампанию, поэтому безопасен и наблюдаем.
func handoffCampaignID(requestID string) domain.CampaignID {
	return domain.CampaignID("handoff-" + requestID)
}

// handoffSpec собирает состав черновика письма: канал email, материал и получатель
// из непрозрачных ссылок, сегментом служит лид (письмо адресовано конкретному лиду).
func handoffSpec(item SubmitHandoffItem, channel domain.ActionChannel) domain.DraftSpec {
	leadID := strings.TrimSpace(item.LeadID)
	return domain.DraftSpec{
		Material:  domain.MaterialID(strings.TrimSpace(item.MaterialRef)),
		Segment:   domain.SegmentID(leadID),
		Recipient: domain.RecipientID(strings.TrimSpace(item.RecipientRef)),
		Channel:   channel,
	}
}

// loadOrCreateHandoffCampaign находит кампанию заявки или создаёт её. Найденная
// кампания чужого тенанта — отказ команды: границу тенанта команда не пересекает.
func loadOrCreateHandoffCampaign(
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
			return nil, false, fmt.Errorf("%w: кампания %s принадлежит другому тенанту", ErrHandoffRejected, id)
		}
		return campaign, true, nil
	}

	window, err := domain.NewCampaignWindow(now, now.Add(handoffCampaignWindow))
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrHandoffRejected, err)
	}
	campaign, err = domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       id,
		TenantID: tenant,
		Name:     name,
		Window:   window,
	})
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrHandoffRejected, err)
	}
	return campaign, false, nil
}

// validateHandoffItems проверяет обязательные ссылки каждого письма, называя все
// нарушения разом.
func validateHandoffItems(items []SubmitHandoffItem) error {
	var invalid []string
	for i, item := range items {
		if strings.TrimSpace(item.LeadID) == "" {
			invalid = append(invalid, fmt.Sprintf("items[%d].lead_id", i))
		}
		if strings.TrimSpace(item.RecipientRef) == "" {
			invalid = append(invalid, fmt.Sprintf("items[%d].recipient_ref", i))
		}
		if strings.TrimSpace(item.MaterialRef) == "" {
			invalid = append(invalid, fmt.Sprintf("items[%d].material_ref", i))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrHandoffRejected, strings.Join(invalid, ", "))
	}
	return nil
}
