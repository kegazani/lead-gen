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
	Editorial bool
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
	editorial := request.ExecutionPurpose() == domain.ExecutionPurposeEditorial
	if editorial && d.Drafts == nil {
		return DraftResult{}, errors.New("редакторская генерация: хранилище черновиков не собрано")
	}

	result := DraftResult{}
	seen := make(map[string]bool, len(in.LeadIDs))
	for _, leadID := range in.LeadIDs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if strings.TrimSpace(leadID) == "" || seen[leadID] {
			continue
		}
		seen[leadID] = true
		email, err := d.Contract.Generate(ctx, emailgeneration.Request{
			RequestID:     in.RequestID,
			TenantID:      in.TenantID,
			LeadID:        leadID,
			Criteria:      criteria,
			SenderCompany: senderCompanyDTO(request.SenderCompanySnapshot()),
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result, err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			result.Failed++
			if editorial {
				// Остальные найденные лиды всё равно обрабатываются; по завершении
				// возвращаем ошибку, не выдавая неполный набор за полный.
			}
			if d.Logger != nil {
				d.Logger.Warn("генерация письма по лиду пропущена", observ.Fields{},
					"request_id", in.RequestID, "lead_id", leadID, "error", err.Error())
			}
			continue
		}
		if d.Drafts == nil {
			return result, errors.New("генерация писем: хранилище черновиков не собрано")
		}
		draft := ports.EmailDraft{
			RequestID:    in.RequestID,
			TenantID:     in.TenantID,
			LeadID:       leadID,
			Subject:      email.Subject,
			Body:         email.Body,
			Language:     email.Language,
			RecipientRef: email.RecipientRef,
		}
		if err := d.Drafts.SaveDrafts(ctx, []ports.EmailDraft{draft}); err != nil {
			return result, fmt.Errorf("сохранение черновика для лида %s: %w", leadID, err)
		}
		result.Drafted++
		result.LeadIDs = append(result.LeadIDs, leadID)
	}
	if editorial && (result.Failed > 0 || result.Drafted == 0) {
		return result, fmt.Errorf("редакторская генерация неполная: создано %d черновиков, ошибок %d", result.Drafted, result.Failed)
	}
	return result, nil
}

func senderCompanyDTO(snapshot *domain.SenderCompanySnapshot) *emailgeneration.SenderCompany {
	if snapshot == nil {
		return nil
	}
	result := &emailgeneration.SenderCompany{
		CompanyName: snapshot.CompanyName, Website: snapshot.Website,
		Description: snapshot.Description, Services: make([]emailgeneration.SenderServiceSnapshot, len(snapshot.Services)),
	}
	for i, service := range snapshot.Services {
		result.Services[i] = emailgeneration.SenderServiceSnapshot{Name: service.Name, Short: service.Short, Full: service.Full}
	}
	return result
}
