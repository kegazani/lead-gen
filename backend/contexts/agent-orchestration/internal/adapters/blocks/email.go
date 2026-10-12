package blocks

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
)

// GenerateEmailBlockRef — ссылка блока производства черновика письма.
const GenerateEmailBlockRef domain.BlockRef = "generate-email"

// defaultContentLanguage — язык материала по умолчанию, если владелец контента не
// задал иначе. Совпадает с языком оффера по умолчанию.
const defaultContentLanguage = "ru"

// defaultContentFormat/defaultContentTemplate — формат и шаблон рендера материала
// письма. Владелец контента (content-production) требует шаблон при производстве
// нового пакета (`ErrTemplateRequired`), поэтому блок обязан его указать; значения
// совпадают с путём доставки писем (заявка → handoff): формат `document`, шаблон
// `doc`, тема письма читается из первой строки тела.
const (
	defaultContentFormat   = "document"
	defaultContentTemplate = "doc"
)

// GenerateEmailExecutor — исполнитель блока generate-email поверх опубликованного
// контракта content-request (владелец — content-production). Блок запрашивает
// производство материала письма под оффер задания и возвращает непрозрачную ссылку
// на версию материала (asset@version); содержимое письма агент не хранит.
//
// Ключ идемпотентности запроса выводится из запуска и шага: повтор шага не создаёт
// вторую версию материала (policy step-idempotency).
type GenerateEmailExecutor struct {
	contract contentrequest.Port
	language string
}

// NewGenerateEmailExecutor собирает исполнитель блока производства материала.
// nil-контракт допустим: шаг честно сообщает недоступность. Пустой язык заменяется
// значением по умолчанию.
func NewGenerateEmailExecutor(contract contentrequest.Port, language string) *GenerateEmailExecutor {
	if strings.TrimSpace(language) == "" {
		language = defaultContentLanguage
	}
	return &GenerateEmailExecutor{contract: contract, language: language}
}

// Ref реализует ports.BlockExecutor.
func (e *GenerateEmailExecutor) Ref() domain.BlockRef { return GenerateEmailBlockRef }

// Execute реализует ports.BlockExecutor.
func (e *GenerateEmailExecutor) Execute(ctx context.Context, in ports.StepInput) (ports.StepResult, error) {
	if e.contract == nil {
		return ports.StepResult{Outcome: domain.Unavailable("content-request: контракт производства контента не подключён")}, nil
	}
	if strings.TrimSpace(in.CompanyID) == "" || strings.TrimSpace(in.Criteria) == "" {
		return ports.StepResult{Outcome: domain.Unavailable("content-request: не заданы компания или оффер задания")}, nil
	}
	result, err := e.contract.RequestContent(ctx, contentrequest.Intent{
		RequestID:     stepIdempotencyKey(in.RunID, in.StepID),
		TenantID:      in.TenantID,
		AssetID:       in.CompanyID,
		Format:        defaultContentFormat,
		TemplateID:    defaultContentTemplate,
		Language:      e.language,
		Body:          in.Criteria,
		Actor:         "agent",
		CorrelationID: in.RunID,
	})
	if err != nil {
		switch {
		case errors.Is(err, contentrequest.ErrRejected):
			return ports.StepResult{Outcome: domain.Failed("content-request: запрос производства отклонён владельцем")}, nil
		case errors.Is(err, contentrequest.ErrUnavailable):
			return ports.StepResult{Outcome: domain.Unavailable("content-request: владелец контента недоступен")}, nil
		default:
			return ports.StepResult{}, err
		}
	}
	if strings.TrimSpace(result.AssetID) == "" || strings.TrimSpace(result.VersionID) == "" {
		return ports.StepResult{Outcome: domain.Unavailable("content-request: владелец не вернул ссылку на версию материала")}, nil
	}
	materialRef := buildMaterialRef(result.AssetID, result.VersionID)
	draftRef, version := splitMaterialRef(materialRef)
	return ports.StepResult{
		Outcome:      domain.Succeeded(),
		ArtifactRef:  materialRef,
		DraftRef:     draftRef,
		DraftVersion: version,
	}, nil
}

// Проверка соответствия порту исполнителя блока.
var _ ports.BlockExecutor = (*GenerateEmailExecutor)(nil)
