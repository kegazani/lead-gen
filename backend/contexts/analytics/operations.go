// Публикация read-контракта campaign-report владельцем analytics.
//
// Экран операций platform-governance собирает цифры процесса и агрегаты по
// клиентам разных владельцев (design.md, Решение 8). Агрегаты процесса —
// данные analytics, поэтому владелец публикует их read-контракт
// (contracts/analytics/campaignreport): in-proc при co-location и по сети
// (campaignreport/httpport) при раздельном запуске.
package analytics

import (
	"context"

	"lead-gen/contracts/analytics/campaignreport"
)

// OperationsPort возвращает read-контракт агрегатов процесса собранного среза.
// Незаданный обработчик честно недоступен (campaignreport.ErrUnavailable), а не
// отдаёт пустой успех: контур не выдумывает цифры.
func (s *Slice) OperationsPort() campaignreport.Port {
	if s == nil || s.Operations == nil {
		return campaignreport.InProc(campaignreport.InProcHandler{})
	}
	handler := s.Operations
	return campaignreport.InProc(campaignreport.InProcHandler{
		NumbersFunc: func(ctx context.Context, request campaignreport.Request) (campaignreport.NumbersResponse, error) {
			return handler.Numbers(ctx, request)
		},
		ClientsFunc: func(ctx context.Context, request campaignreport.Request) (campaignreport.ClientsResponse, error) {
			return handler.Clients(ctx, request)
		},
	})
}

// OperationsPort возвращает read-контракт агрегатов процесса собранного сервиса:
// точка входа связывает его с потребителем (platform-governance) без раскрытия
// внутренних типов контекста.
func (s *Service) OperationsPort() campaignreport.Port {
	if s == nil {
		return campaignreport.InProc(campaignreport.InProcHandler{})
	}
	return s.slice.OperationsPort()
}
