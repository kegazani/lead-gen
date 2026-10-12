package platformgovernance

import (
	"context"
	"time"

	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/contracts/analytics/campaignreport"
)

// Адаптер агрегатов процесса к опубликованному read-контракту campaign-report
// (владелец analytics). Экран операций читает цифры процесса и агрегаты по
// клиентам у владельца: при co-location — прямой вызов (campaignreport.InProc,
// его собирает владелец), при раздельном запуске — сетевой клиент
// (campaignreport/httpport). Адаптер только переводит форму контракта в форму
// экрана; недоступность владельца возвращается ошибкой и показывается честно
// (AGENTS.md §3).

// NewOperationsAggregates связывает read-контракт campaign-report с источником
// агрегатов экрана операций. nil-порт означает, что владелец не подключён:
// возвращается nil, и раздел цифр/дашборда честно недоступен, а не пуст.
func NewOperationsAggregates(port campaignreport.Port) OperationsAggregates {
	if port == nil {
		return nil
	}
	return contractAggregates{port: port}
}

// contractAggregates реализует OperationsAggregates поверх контракта.
type contractAggregates struct {
	port campaignreport.Port
}

// Проверка соответствия контракту потребителя на этапе компиляции.
var _ OperationsAggregates = contractAggregates{}

// Numbers читает цифры процесса по клиентам и разбивку по времени.
func (a contractAggregates) Numbers(ctx context.Context, window time.Duration) (httpapi.ClientNumbersResult, error) {
	response, err := a.port.Numbers(ctx, campaignreport.Request{Window: window})
	if err != nil {
		return httpapi.ClientNumbersResult{}, err
	}
	result := httpapi.ClientNumbersResult{
		Clients: make([]httpapi.ClientNumbers, 0, len(response.Clients)),
		Periods: make([]httpapi.NumberPeriod, 0, len(response.Periods)),
	}
	for _, client := range response.Clients {
		result.Clients = append(result.Clients, httpapi.ClientNumbers{
			ClientID:   client.ClientID,
			Found:      client.Found,
			Processed:  client.Processed,
			Rejected:   client.Rejected,
			InProgress: client.InProgress,
			Result:     client.Result,
		})
	}
	for _, period := range response.Periods {
		result.Periods = append(result.Periods, httpapi.NumberPeriod{
			Date:      period.Date,
			Found:     period.Found,
			Processed: period.Processed,
			Rejected:  period.Rejected,
		})
	}
	return result, nil
}

// Clients читает агрегаты по клиентам для полноэкранного дашборда. Срез кодов
// аномалий копируется: адаптер не делит его с источником.
func (a contractAggregates) Clients(ctx context.Context, window time.Duration) ([]httpapi.ClientAggregate, error) {
	response, err := a.port.Clients(ctx, campaignreport.Request{Window: window})
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.ClientAggregate, 0, len(response.Clients))
	for _, client := range response.Clients {
		out = append(out, httpapi.ClientAggregate{
			ClientID:   client.ClientID,
			Found:      client.Found,
			Processed:  client.Processed,
			Rejected:   client.Rejected,
			InProgress: client.InProgress,
			Result:     client.Result,
			Anomalies:  append([]string(nil), client.Anomalies...),
		})
	}
	return out, nil
}
