package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// Параметры производства материала письма контрактом content-request. Формат
// `document` и шаблон `doc` — путь доставки читает тему из первой строки тела;
// язык по умолчанию — русский, если черновик его не назвал.
const (
	handoffMaterialFormat   = "document"
	handoffMaterialTemplate = "doc"
	handoffDefaultLanguage  = "ru"
)

// HandoffInput — вход шага передачи черновиков писем в outreach-orchestration.
// Только идентификаторы и короткие ссылки: текст писем activity читает из
// хранилища черновиков, поэтому в payload workflow он не поднимается.
type HandoffInput struct {
	RequestID string
	TenantID  string
	Actor     string
	Name      string
}

// HandoffOutcome — наблюдаемый исход шага передачи: сколько писем поставлено в
// очередь утверждения, сколько честно пропущено и был ли это повтор. CampaignID и
// Expected позволяют заявке зафиксировать кампанию и ожидать доставку именно
// стольких писем (capability request-delivery-conclusion).
type HandoffOutcome struct {
	// Handed — число переданных (принятых потребителем) писем.
	Handed int
	// Skipped — число пропущенных писем: без получателя либо отвергнутых приёмом.
	Skipped int
	// Repeated — признак повтора: кампания заявки уже существовала.
	Repeated bool
	// CampaignID — идентификатор кампании заявки в outreach-orchestration. Пусто,
	// если кампания не создавалась (нет писем с разрешимым получателем).
	CampaignID string
	// Expected — число писем, реально переданных на утверждение (устойчиво к
	// повтору, в отличие от Handed). По нему заявка считает полное покрытие
	// доставкой.
	Expected int
}

// HandoffSender — источник шага передачи черновиков писем: либо адаптер
// опубликованных контрактов (ContractHandoff). nil-источник означает, что передача
// не подключена: шаг штатно пропускается.
type HandoffSender interface {
	Handoff(ctx context.Context, in HandoffInput) (HandoffOutcome, error)
}

// ContractHandoff — HandoffSender поверх опубликованных контрактов: читает
// черновики заявки из хранилища контекста, для каждого письма с непустой ссылкой
// на получателя производит материал контрактом content-request (владелец —
// content-production) и передаёт состав одной командой по контракту
// outreach-handoff (потребитель — outreach-orchestration).
//
// Отсутствие контракта материала или передачи (nil) — штатный пропуск с логом, а
// не падение и не выдуманная передача. Письмо без получателя честно пропускается:
// фиктивный адрес не подставляется (AGENTS.md §3). Ошибка владельца материала или
// приёмника не маскируется успехом — она возвращается наружу.
type ContractHandoff struct {
	// Drafts — хранилище черновиков писем оператора.
	Drafts ports.EmailDraftStore
	// Content — порт опубликованного контракта content-request.
	Content contentrequest.Port
	// Outreach — порт опубликованного контракта outreach-handoff.
	Outreach outreachhandoff.Port
	// Logger — логгер сервиса. nil — без логирования исходов.
	Logger observ.Logger
}

// Handoff реализует HandoffSender.
func (h ContractHandoff) Handoff(ctx context.Context, in HandoffInput) (HandoffOutcome, error) {
	if h.Content == nil || h.Outreach == nil {
		h.logSkip(in.RequestID, "контракт материала или передачи не подключён")
		return HandoffOutcome{}, nil
	}
	if h.Drafts == nil {
		return HandoffOutcome{}, errors.New("передача писем: хранилище черновиков не собрано")
	}
	drafts, err := h.Drafts.ListByRequest(ctx, in.RequestID, in.TenantID)
	if err != nil {
		return HandoffOutcome{}, fmt.Errorf("чтение черновиков заявки %s: %w", in.RequestID, err)
	}

	items := make([]outreachhandoff.Item, 0, len(drafts))
	skipped := 0
	for _, draft := range drafts {
		if strings.TrimSpace(draft.RecipientRef) == "" {
			// Нет пригодного адреса — письмо не передаётся. Пропуск наблюдаем, а
			// адрес не выдумывается.
			skipped++
			if h.Logger != nil {
				h.Logger.Warn("передача письма пропущена: получатель не разрешён", observ.Fields{},
					"request_id", in.RequestID, "lead_id", draft.LeadID)
			}
			continue
		}
		material, err := h.produceMaterial(ctx, in, draft)
		if err != nil {
			return HandoffOutcome{}, err
		}
		items = append(items, outreachhandoff.Item{
			LeadID:       draft.LeadID,
			RecipientRef: draft.RecipientRef,
			MaterialRef:  material,
		})
	}

	if len(items) == 0 {
		h.logSkip(in.RequestID, "нет писем с разрешимым получателем")
		return HandoffOutcome{Skipped: skipped}, nil
	}

	// Actor пуст: передача — машинная межсервисная команда от имени тенанта
	// заявки, а не решение оператора. Приёмник при пустом акторе проверку права
	// оператора не выполняет (capability outreach-handoff), а границу тенанта держит
	// агрегат. Актор материала (`in.Actor`) остаётся у content-request — там он нужен
	// как происхождение и обязателен.
	command := outreachhandoff.Command{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		Actor:     "",
		Name:      in.Name,
		Items:     items,
	}
	if err := command.Validate(); err != nil {
		return HandoffOutcome{}, fmt.Errorf("команда передачи заявки %s: %w", in.RequestID, err)
	}
	result, err := h.Outreach.Submit(ctx, command)
	if err != nil {
		return HandoffOutcome{}, fmt.Errorf("передача писем заявки %s: %w", in.RequestID, err)
	}
	if h.Logger != nil {
		h.Logger.Info("письма заявки переданы на утверждение", observ.Fields{},
			"request_id", in.RequestID, "accepted", result.Accepted,
			"skipped", result.Skipped, "repeated", result.Repeated)
	}
	return HandoffOutcome{
		Handed:     result.Accepted,
		Skipped:    skipped + result.Skipped,
		Repeated:   result.Repeated,
		CampaignID: result.CampaignID,
		Expected:   len(items),
	}, nil
}

// produceMaterial производит материал письма контрактом content-request и
// возвращает непрозрачную ссылку `asset_id@version_id`. Тело материала — тема и
// тело письма, разделённые пустой строкой: тему путь доставки читает из первой
// строки материала. AssetID и RequestID выведены детерминированно из заявки и
// лида, поэтому повтор идемпотентен.
func (h ContractHandoff) produceMaterial(ctx context.Context, in HandoffInput, draft ports.EmailDraft) (string, error) {
	result, err := h.Content.RequestContent(ctx, contentrequest.Intent{
		RequestID:  in.RequestID + "-" + draft.LeadID,
		TenantID:   in.TenantID,
		AssetID:    emailMaterialAssetID(in.RequestID, draft.LeadID),
		Format:     handoffMaterialFormat,
		Language:   handoffLanguage(draft.Language),
		TemplateID: handoffMaterialTemplate,
		Body:       draft.Subject + "\n\n" + draft.Body,
		Actor:      in.Actor,
	})
	if err != nil {
		return "", fmt.Errorf("производство материала письма по лиду %s: %w", draft.LeadID, err)
	}
	if strings.TrimSpace(result.AssetID) == "" || strings.TrimSpace(result.VersionID) == "" {
		return "", fmt.Errorf("производство материала письма по лиду %s: владелец не вернул ссылку на материал", draft.LeadID)
	}
	return result.AssetID + "@" + result.VersionID, nil
}

// logSkip логирует штатный пропуск шага передачи, не выдавая его за успех.
func (h ContractHandoff) logSkip(requestID, reason string) {
	if h.Logger == nil {
		return
	}
	h.Logger.Info("передача писем пропущена", observ.Fields{},
		"request_id", requestID, "reason", reason)
}

// emailMaterialAssetID выводит детерминированный идентификатор материала письма
// из заявки и лида: повтор команды не создаёт второй материал.
func emailMaterialAssetID(requestID, leadID string) string {
	return "email-" + requestID + "-" + leadID
}

// handoffLanguage берёт язык черновика, а при пустом — язык по умолчанию.
func handoffLanguage(language string) string {
	if strings.TrimSpace(language) == "" {
		return handoffDefaultLanguage
	}
	return language
}
