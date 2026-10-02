// Package call — адаптеры порта инициирования звонка-уточнения.
//
// Владение звонком остаётся у outreach-orchestration: заявка хранит только
// идентификатор. Адаптер Contract переводит порт контекста в опубликованный
// контракт call-intent; Unavailable честно сообщает, что провайдер не подключён, и
// ничего не выдумывает.
package call

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/callintent"
)

// Contract — адаптер порта звонков к опубликованному контракту call-intent.
type Contract struct {
	port callintent.Port
}

// Contract реализует порт инициирования звонка.
var _ ports.CallInitiator = (*Contract)(nil)

// NewContract собирает адаптер поверх контракта владельца звонка.
func NewContract(port callintent.Port) *Contract {
	return &Contract{port: port}
}

// InitiateCall вызывает контракт и переводит его ошибки в ошибки порта: доменный
// отказ владельца — ErrCallRejected, сбой транспорта — ErrCallUnavailable.
func (c *Contract) InitiateCall(ctx context.Context, in ports.InitiateCall) (ports.CallResult, error) {
	if c == nil || c.port == nil {
		return ports.CallResult{}, ports.ErrCallUnavailable
	}
	result, err := c.port.Initiate(ctx, callintent.Intent{
		RequestID:     in.RequestID,
		TenantID:      in.TenantID,
		CorrelationID: in.CorrelationID,
		Purpose:       callintent.Purpose(in.Purpose),
	})
	if err != nil {
		if errors.Is(err, callintent.ErrRejected) {
			return ports.CallResult{}, fmt.Errorf("%w: %v", ports.ErrCallRejected, err)
		}
		return ports.CallResult{}, fmt.Errorf("%w: %v", ports.ErrCallUnavailable, err)
	}
	if strings.TrimSpace(result.CallID) == "" {
		return ports.CallResult{}, fmt.Errorf("%w: владелец вернул пустой идентификатор звонка", ports.ErrCallUnavailable)
	}
	return ports.CallResult{CallID: result.CallID}, nil
}
