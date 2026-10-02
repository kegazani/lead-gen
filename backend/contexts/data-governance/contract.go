package datagovernance

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/data-governance/internal/app/query"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/contracts/data-governance/datazonepolicy"
	"lead-gen/contracts/data-governance/processingauthority"
	"lead-gen/contracts/data-governance/suppressionpolicy"
)

// Адаптеры опубликованных контрактов контекста data-governance.
//
// Контракты выражены интерфейсами в отдельном модуле contracts/data-governance,
// а не пакетами владельца: потребитель держит форму контракта и не видит
// внутренних пакетов контекста. Точка сборки сервиса (этот пакет) связывает
// потребителя с реализацией владельца: при co-location — прямым вызовом, при
// разделении сервисов — сетевым клиентом (design.md, Решение 2).
//
// Транспорт в каталоге — async/amqp (интеграция policy). Сетевой адаптер в объёме
// среза НЕ реализован: это точка расширения, а не работающий транспорт.

// NewZonePolicy собирает контракт data-zone-policy поверх среза контекста.
func NewZonePolicy(slice *Slice) datazonepolicy.Port {
	if slice == nil {
		return datazonepolicy.InProc(datazonepolicy.InProcHandler{})
	}
	return datazonepolicy.InProc(datazonepolicy.InProcHandler{
		DecideFunc: func(ctx context.Context, q datazonepolicy.ZoneQuery) (datazonepolicy.ZoneDecision, error) {
			view, err := slice.CheckZone.Handle(ctx, query.CheckZone{
				TenantID: domain.TenantID(q.TenantID),
				Purpose:  q.Purpose,
				Category: domain.DataCategory(q.Category),
				At:       q.At,
			})
			if err != nil {
				return datazonepolicy.ZoneDecision{}, unavailable(datazonepolicy.ErrUnavailable, err)
			}
			return datazonepolicy.ZoneDecision{
				Authorized:           view.Authorized,
				Category:             string(view.Category),
				Handling:             string(view.Handling),
				SharedStorageAllowed: view.SharedStorageAllowed,
				Minimize:             view.Minimize,
				Reason:               view.Reason,
			}, nil
		},
	})
}

// NewProcessingAuthority собирает контракт processing-authority поверх среза
// контекста.
func NewProcessingAuthority(slice *Slice) processingauthority.Port {
	if slice == nil {
		return processingauthority.InProc(processingauthority.InProcHandler{})
	}
	return processingauthority.InProc(processingauthority.InProcHandler{
		CheckFunc: func(ctx context.Context, q processingauthority.AuthorityQuery) (processingauthority.Authority, error) {
			view, err := slice.CheckAuthority.Handle(ctx, query.CheckAuthority{
				TenantID: domain.TenantID(q.TenantID),
				Purpose:  q.Purpose,
				Category: domain.DataCategory(q.Category),
				At:       q.At,
			})
			if err != nil {
				return processingauthority.Authority{}, unavailable(processingauthority.ErrUnavailable, err)
			}
			if !view.Authorized {
				return processingauthority.Authority{}, fmt.Errorf(
					"%w: тенант %s, цель %q, категория %q",
					processingauthority.ErrNotFound, q.TenantID, q.Purpose, q.Category,
				)
			}
			return processingauthority.Authority{
				BasisID:       view.BasisID,
				TenantID:      view.TenantID,
				Purpose:       view.Purpose,
				Category:      string(view.Category),
				Authorized:    true,
				RetentionDays: view.RetentionDays,
				ValidFrom:     view.ValidFrom,
				ValidUntil:    view.ValidUntil,
				Status:        string(view.Status),
			}, nil
		},
	})
}

// NewSuppressionPolicy собирает контракт suppression-policy поверх среза
// контекста.
func NewSuppressionPolicy(slice *Slice) suppressionpolicy.Port {
	if slice == nil {
		return suppressionpolicy.InProc(suppressionpolicy.InProcHandler{})
	}
	return suppressionpolicy.InProc(suppressionpolicy.InProcHandler{
		CheckFunc: func(ctx context.Context, q suppressionpolicy.SuppressionQuery) (suppressionpolicy.SuppressionDecision, error) {
			view, err := slice.CheckSuppress.Handle(ctx, query.CheckSuppression{
				TenantID: domain.TenantID(q.TenantID),
				Contact:  domain.ContactRef(q.Contact),
			})
			if err != nil {
				return suppressionpolicy.SuppressionDecision{}, unavailable(suppressionpolicy.ErrUnavailable, err)
			}
			return suppressionpolicy.SuppressionDecision{
				Suppressed:    view.Suppressed,
				Reason:        view.Reason,
				SuppressionID: view.SuppressionID,
				BasisID:       view.BasisID,
				Since:         view.Since,
			}, nil
		},
	})
}

// unavailable оборачивает ошибку владельца исходом недоступности контракта.
func unavailable(sentinel error, err error) error {
	return fmt.Errorf("%w: %v", sentinel, err)
}
