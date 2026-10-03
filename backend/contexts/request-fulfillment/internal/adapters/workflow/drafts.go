package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/lead-management/emailgeneration"
)

// DraftInput — вход шага генерации черновиков писем. Только идентификаторы:
// критерий-оффер activity читает из заявки сама, поэтому текст оффера в payload
// workflow не поднимается.
type DraftInput struct {
	RequestID string
	TenantID  string
	LeadIDs   []string
}

// DraftResult — результат шага генерации черновиков: сколько писем сохранено,
// сколько лидов пропущено и под какие лиды есть черновики. Текста писем здесь нет
// по определению payload-гигиены: он лежит в хранилище черновиков.
type DraftResult struct {
	// Drafted — число сохранённых черновиков.
	Drafted int
	// Failed — число лидов, под которые письмо сгенерировать не удалось.
	Failed int
	// LeadIDs — идентификаторы лидов, под которые черновик сохранён.
	LeadIDs []string
}

// DraftGenerator — источник шага генерации черновиков писем: либо адаптер
// опубликованного контракта email-generation (ContractDrafts). nil-источник
// означает, что контракт не подключён: шаг штатно пропускается.
type DraftGenerator interface {
	Draft(ctx context.Context, in DraftInput) (DraftResult, error)
}

// ContractDrafts — DraftGenerator поверх опубликованного контракта
// email-generation (владелец — lead-management). Заявку читает из репозитория
// контекста, оффер собирает в критерий, а у владельца просит письмо под каждого
// найденного лида. Текст письма наружу не поднимается: он сохраняется в хранилище
// черновиков, а activity возвращает только счётчики и идентификаторы лидов.
type ContractDrafts struct {
	// Requests — репозиторий заявок: источник оффера по RequestID.
	Requests ports.RequestRepository
	// Contract — порт опубликованного контракта email-generation.
	Contract emailgeneration.Port
	// Drafts — хранилище черновиков писем оператора.
	Drafts ports.EmailDraftStore
	// Logger — логгер сервиса. nil — без логирования исходов по лиду.
	Logger observ.Logger
}

// Draft реализует DraftGenerator: под каждого найденного лида вызывает контракт и
// сохраняет письмо в хранилище черновиков.
//
// Ошибка генерации по одному лиду не роняет шаг: письма под остальных лидов
// генерируются, а исход виден счётчиком Failed. Так один «плохой» лид (пустой
// критерий, доменный отказ владельца) не блокирует заявку целиком, а оператор всё
// равно получает частичный набор черновиков. Ошибка чтения заявки и ошибка
// сохранения черновиков — честные ошибки шага: без заявки нет оффера, а без
// хранилища теряется весь результат.
func (d ContractDrafts) Draft(ctx context.Context, in DraftInput) (DraftResult, error) {
	if d.Requests == nil {
		return DraftResult{}, errors.New("генерация писем: репозиторий заявок не собран")
	}
	if d.Contract == nil {
		return DraftResult{}, fmt.Errorf("%w: контракт email-generation не подключён", emailgeneration.ErrUnavailable)
	}
	request, err := d.Requests.FindByID(ctx, domain.RequestID(in.RequestID))
	if err != nil {
		return DraftResult{}, fmt.Errorf("чтение заявки %s: %w", in.RequestID, err)
	}
	criteria := criteriaFromOffer(request.Offer())

	result := DraftResult{}
	drafts := make([]ports.EmailDraft, 0, len(in.LeadIDs))
	seen := make(map[string]bool, len(in.LeadIDs))
	for _, leadID := range in.LeadIDs {
		if strings.TrimSpace(leadID) == "" || seen[leadID] {
			continue
		}
		seen[leadID] = true
		email, err := d.Contract.Generate(ctx, emailgeneration.Request{
			RequestID: in.RequestID,
			TenantID:  in.TenantID,
			LeadID:    leadID,
			Criteria:  criteria,
		})
		if err != nil {
			result.Failed++
			if d.Logger != nil {
				d.Logger.Warn("генерация письма по лиду пропущена", observ.Fields{},
					"request_id", in.RequestID, "lead_id", leadID, "error", err.Error())
			}
			continue
		}
		drafts = append(drafts, ports.EmailDraft{
			RequestID: in.RequestID,
			TenantID:  in.TenantID,
			LeadID:    leadID,
			Subject:   email.Subject,
			Body:      email.Body,
			Language:  email.Language,
		})
		result.Drafted++
		result.LeadIDs = append(result.LeadIDs, leadID)
	}
	if len(drafts) > 0 {
		if d.Drafts == nil {
			return DraftResult{}, errors.New("генерация писем: хранилище черновиков не собрано")
		}
		if err := d.Drafts.SaveDrafts(ctx, drafts); err != nil {
			return DraftResult{}, fmt.Errorf("сохранение черновиков писем: %w", err)
		}
	}
	return result, nil
}
