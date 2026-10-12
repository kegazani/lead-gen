package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегата и его сущностей.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет RequestSnapshot с экспортированными полями. Доменный объект из
// снимка собирается только через RehydrateRequest, который проверяет инварианты
// и не эмитит события: восстановление не является новым фактом домена.

// OfferSnapshot — сохранённое состояние версии оффера.
type OfferSnapshot struct {
	ID        OfferID
	Content   OfferContent
	Target    RequestTarget
	Revision  int
	Embedding []float32
}

// TransitionSnapshot — сохранённая запись перехода статуса.
type TransitionSnapshot struct {
	From       RequestStatus
	To         RequestStatus
	Actor      ActorID
	At         time.Time
	Reason     string
	ReasonCode string
}

// RequestSnapshot — полное сохранённое состояние aggregate root request.
// OfferHistory хранит прежние версии оффера; Transitions сохраняют порядок
// переходов.
type RequestSnapshot struct {
	ID                 RequestID
	TenantID           TenantID
	Status             RequestStatus
	Version            int
	Offer              OfferSnapshot
	OfferHistory       []OfferSnapshot
	AudienceSnapshotID AudienceSnapshotID
	CampaignID         CampaignID
	DealID             DealID
	CallID             CallID
	// ExecutionHistoryKnown добавлено как явный маркер современных snapshots.
	// Отсутствующее значение в legacy JSON остаётся false, а не выводится из статуса.
	ExecutionHistoryKnown bool                   `json:",omitempty"`
	ExecutionPurpose      string                 `json:"ExecutionPurpose,omitempty"`
	SenderCompany         *SenderCompanySnapshot `json:"SenderCompany,omitempty"`
	ValidationDecision    *ValidationDecisionSnapshot
	Transitions           []TransitionSnapshot
}

// Snapshot возвращает состояние агрегата для сохранения.
func (r *Request) Snapshot() RequestSnapshot {
	history := make([]OfferSnapshot, 0, len(r.offerHistory))
	for i := range r.offerHistory {
		history = append(history, r.offerHistory[i].Snapshot())
	}
	transitions := make([]TransitionSnapshot, 0, len(r.transitions))
	for _, t := range r.transitions {
		transitions = append(transitions, TransitionSnapshot{
			From:       t.from,
			To:         t.to,
			Actor:      t.actor,
			At:         t.at,
			Reason:     t.reason,
			ReasonCode: t.reasonCode,
		})
	}
	return RequestSnapshot{
		ID:                    r.id,
		TenantID:              r.tenantID,
		Status:                r.status,
		Version:               r.version,
		Offer:                 r.offer.Snapshot(),
		OfferHistory:          history,
		AudienceSnapshotID:    r.audienceSnapshotID,
		CampaignID:            r.campaignID,
		DealID:                r.dealID,
		CallID:                r.callID,
		ExecutionHistoryKnown: r.executionHistoryKnown,
		ExecutionPurpose:      persistedExecutionPurpose(r.executionPurpose),
		SenderCompany:         cloneSenderCompany(r.senderCompany),
		ValidationDecision:    decisionSnapshot(r.validationDecision),
		Transitions:           transitions,
	}
}

// decisionSnapshot переводит решение валидации в снимок, сохраняя отсутствие
// решения как nil.
func decisionSnapshot(d *ValidationDecision) *ValidationDecisionSnapshot {
	if d == nil {
		return nil
	}
	snapshot := d.Snapshot()
	return &snapshot
}

// RehydrateRequest восстанавливает aggregate root из сохранённого состояния,
// проверяя инварианты: известность и терминальность статуса, непустой оффер,
// положительную версию, рост ревизий оффера и непустой журнал переходов.
// События не эмитятся.
func RehydrateRequest(s RequestSnapshot) (*Request, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if !s.Status.isValid() {
		return nil, fmt.Errorf("%w: %q у заявки %s", ErrUnknownRequestStatus, s.Status, s.ID)
	}
	purpose := normalizeExecutionPurpose(s.ExecutionPurpose)
	if purpose != ExecutionPurposeDelivery && purpose != ExecutionPurposeEditorial {
		return nil, fmt.Errorf("%w: заявка %s: недопустимый purpose %q", ErrInvalidSnapshot, s.ID, purpose)
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: заявка %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	offer, err := rehydrateOffer(s.Offer)
	if err != nil {
		return nil, fmt.Errorf("%w: заявка %s: %v", ErrInvalidSnapshot, s.ID, err)
	}

	history := make([]Offer, 0, len(s.OfferHistory))
	previousRevision := 0
	for _, os := range s.OfferHistory {
		rehydrated, err := rehydrateOffer(os)
		if err != nil {
			return nil, fmt.Errorf("%w: заявка %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if rehydrated.revision <= previousRevision {
			return nil, fmt.Errorf(
				"%w: заявка %s: ревизии оффера не возрастают (%d после %d)",
				ErrInvalidSnapshot, s.ID, rehydrated.revision, previousRevision,
			)
		}
		previousRevision = rehydrated.revision
		history = append(history, *rehydrated)
	}
	if previousRevision >= offer.revision {
		return nil, fmt.Errorf(
			"%w: заявка %s: текущая ревизия оффера %d не больше прежней %d",
			ErrInvalidSnapshot, s.ID, offer.revision, previousRevision,
		)
	}

	if len(s.Transitions) == 0 {
		return nil, fmt.Errorf("%w: заявка %s без журнала переходов", ErrInvalidSnapshot, s.ID)
	}
	transitions := make([]Transition, 0, len(s.Transitions))
	var last RequestStatus
	for _, ts := range s.Transitions {
		if ts.To == "" || !ts.To.isValid() {
			return nil, fmt.Errorf("%w: заявка %s: неизвестный статус перехода %q", ErrInvalidSnapshot, s.ID, ts.To)
		}
		if ts.At.IsZero() {
			return nil, fmt.Errorf("%w: заявка %s: переход без времени", ErrInvalidSnapshot, s.ID)
		}
		transitions = append(transitions, Transition{
			from:       ts.From,
			to:         ts.To,
			actor:      ts.Actor,
			at:         ts.At,
			reason:     ts.Reason,
			reasonCode: ts.ReasonCode,
		})
		last = ts.To
	}
	if last != s.Status {
		return nil, fmt.Errorf(
			"%w: заявка %s: последний переход %q не совпадает со статусом %q",
			ErrInvalidSnapshot, s.ID, last, s.Status,
		)
	}

	decision, err := rehydrateDecision(s.ID, s.Status, s.ValidationDecision)
	if err != nil {
		return nil, err
	}

	return &Request{
		id:                    s.ID,
		tenantID:              s.TenantID,
		status:                s.Status,
		offer:                 offer,
		offerHistory:          history,
		audienceSnapshotID:    s.AudienceSnapshotID,
		campaignID:            s.CampaignID,
		dealID:                s.DealID,
		callID:                s.CallID,
		executionHistoryKnown: s.ExecutionHistoryKnown,
		executionPurpose:      purpose,
		senderCompany:         cloneSenderCompany(s.SenderCompany),
		validationDecision:    decision,
		transitions:           transitions,
		version:               s.Version,
	}, nil
}

// Не добавляем синтетическое значение delivery в legacy snapshot: отсутствие
// поля сохраняет побайтно прежнюю форму JSON и читается как delivery.
func persistedExecutionPurpose(purpose string) string {
	purpose = normalizeExecutionPurpose(purpose)
	if purpose == ExecutionPurposeDelivery {
		return ""
	}
	return purpose
}

// rehydrateDecision восстанавливает решение валидации и сверяет его со статусом:
// validated и rejected невозможны без решения, а решение обязано совпадать с
// исходом перехода.
func rehydrateDecision(id RequestID, status RequestStatus, snapshot *ValidationDecisionSnapshot) (*ValidationDecision, error) {
	if snapshot == nil {
		if status == StatusValidated || status == StatusRejected {
			return nil, fmt.Errorf("%w: заявка %s в статусе %s без решения валидации", ErrInvalidSnapshot, id, status)
		}
		return nil, nil
	}
	decision, err := rehydrateValidationDecision(*snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: заявка %s: %v", ErrInvalidSnapshot, id, err)
	}
	if status == StatusValidated && !decision.Approved() {
		return nil, fmt.Errorf("%w: заявка %s в статусе %s с решением %q", ErrInvalidSnapshot, id, status, decision.Outcome())
	}
	if status == StatusRejected && decision.Approved() {
		return nil, fmt.Errorf("%w: заявка %s в статусе %s с решением %q", ErrInvalidSnapshot, id, status, decision.Outcome())
	}
	return &decision, nil
}
