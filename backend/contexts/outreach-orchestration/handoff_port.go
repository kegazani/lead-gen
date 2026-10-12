package outreachorchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// HandoffPort возвращает контрактную форму приёма передачи писем заявки
// (capability outreach-handoff) для co-location: тонкий перевод типов контракта в
// прикладную команду среза. Несобранный срез или отсутствующий сценарий дают
// честную недоступность контракта, а не выдуманный приём.
//
// Продуктовый путь на раздельной топологии использует сетевой клиент
// outreachhandoff/httpport; эта форма нужна, когда request-fulfillment и
// outreach-orchestration co-located в одном процессе.
func (s *Service) HandoffPort() outreachhandoff.Port {
	if s == nil || s.slice == nil || s.slice.Handoff == nil {
		return outreachhandoff.InProc(outreachhandoff.InProcHandler{})
	}
	handler := s.slice.Handoff
	return outreachhandoff.InProc(outreachhandoff.InProcHandler{
		SubmitFunc: func(ctx context.Context, c outreachhandoff.Command) (outreachhandoff.Result, error) {
			if c.ExecutionPurpose == outreachhandoff.ExecutionPurposeEditorial {
				if s.slice.EditorialReview == nil {
					return outreachhandoff.Result{}, outreachhandoff.ErrUnavailable
				}
				items := make([]appeditorial.Item, 0, len(c.Items))
				for _, item := range c.Items {
					parts := strings.SplitN(item.MaterialRef, "@", 2)
					if strings.TrimSpace(item.LeadID) == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
						return outreachhandoff.Result{}, outreachhandoff.ErrRejected
					}
					items = append(items, appeditorial.Item{LeadID: item.LeadID, MaterialRef: item.MaterialRef, AssetID: parts[0], VersionID: parts[1]})
				}
				campaign, repeated, err := s.slice.EditorialReview.Submit(ctx, appeditorial.Handoff{CampaignID: "handoff-" + c.RequestID, TenantID: c.TenantID, RequestID: c.RequestID, Items: items})
				if err != nil {
					return outreachhandoff.Result{}, handoffContractError(err)
				}
				accepted, skipped := len(items), 0
				if repeated {
					accepted = 0
					skipped = len(items)
				}
				return outreachhandoff.Result{CampaignID: campaign.CampaignID, Accepted: accepted, Skipped: skipped, Repeated: repeated}, nil
			}
			in := command.SubmitHandoff{
				RequestID:        c.RequestID,
				TenantID:         c.TenantID,
				Actor:            c.Actor,
				Name:             c.Name,
				ExecutionPurpose: c.ExecutionPurpose,
				Items:            handoffItems(c.Items),
			}
			result, err := handler.Handle(ctx, in)
			if err != nil {
				return outreachhandoff.Result{}, handoffContractError(err)
			}
			return outreachhandoff.Result{
				CampaignID: string(result.CampaignID),
				Accepted:   result.Accepted,
				Skipped:    result.Skipped,
				Repeated:   result.Repeated,
			}, nil
		},
		CancelFunc: func(ctx context.Context, cancellation outreachhandoff.RequestCancellation) error {
			if s.slice.CancelRequest == nil {
				return outreachhandoff.ErrUnavailable
			}
			if err := s.slice.CancelRequest.Handle(ctx, command.CancelRequest{
				TenantID: cancellation.TenantID, RequestID: cancellation.RequestID,
			}); err != nil {
				return fmt.Errorf("%w: сохранение отмены заявки: %v", outreachhandoff.ErrUnavailable, err)
			}
			return nil
		},
	})
}

// handoffItems переводит состав контракта в состав прикладной команды.
func handoffItems(items []outreachhandoff.Item) []command.SubmitHandoffItem {
	out := make([]command.SubmitHandoffItem, 0, len(items))
	for _, item := range items {
		out = append(out, command.SubmitHandoffItem{
			LeadID:       item.LeadID,
			RecipientRef: item.RecipientRef,
			MaterialRef:  item.MaterialRef,
		})
	}
	return out
}

// handoffContractError переводит ошибку сценария в исход контракта: доменный
// отказ различим, прочее (сбой хранилища, транзакции, доступа) — недоступность.
// Недоступность не выдаётся за успешный приём.
func handoffContractError(err error) error {
	if errors.Is(err, command.ErrHandoffRejected) {
		return fmt.Errorf("%w: %v", outreachhandoff.ErrRejected, err)
	}
	return fmt.Errorf("%w: %v", outreachhandoff.ErrUnavailable, err)
}
