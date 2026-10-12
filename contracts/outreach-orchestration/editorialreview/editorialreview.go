// Package editorialreview публикует чтение точной версии editorial material и
// отдельную команду фиксации решения. Контракт не представляет разрешение на
// доставку и не зависит от модели чужого bounded context.
package editorialreview

import (
	"context"
	"errors"
)

const ContractName = "editorial-review"
const SchemaVersion = "1.0.0"

var (
	ErrNotFound    = errors.New("editorialreview: версия не найдена")
	ErrConflict    = errors.New("editorialreview: решение конфликтует с сохранённым состоянием")
	ErrRejected    = errors.New("editorialreview: команда отклонена")
	ErrUnavailable = errors.New("editorialreview: владелец недоступен")
)

type Query struct {
	TenantID    string
	CampaignID  string
	LeadID      string
	MaterialRef string
	VersionID   string
}

type Decision struct {
	EventType string
	Outcome     string
	Reason      string
	Actor       string
	DecidedAt   string
	VersionID   string
	MaterialRef string
}

type Record struct {
	Query
	Subject  string
	Body     string
	Decision *Decision
}

type Command struct {
	Query
	ExpectedVersion string
	Outcome         string
	Reason          string
	Actor           string
}

type Port interface {
	Read(context.Context, Query) (Record, error)
	Decide(context.Context, Command) (Record, error)
}

type InProcHandler struct {
	ReadFunc   func(context.Context, Query) (Record, error)
	DecideFunc func(context.Context, Command) (Record, error)
}

func (h InProcHandler) Read(ctx context.Context, q Query) (Record, error) {
	if h.ReadFunc == nil {
		return Record{}, ErrUnavailable
	}
	return h.ReadFunc(ctx, q)
}

func (h InProcHandler) Decide(ctx context.Context, c Command) (Record, error) {
	if h.DecideFunc == nil {
		return Record{}, ErrUnavailable
	}
	return h.DecideFunc(ctx, c)
}

func InProc(h InProcHandler) Port { return h }
