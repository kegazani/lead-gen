package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегатов.
//
// Адаптеры не имеют доступа к закрытым полям, поэтому читают и пишут снимки с
// экспортированными полями. Доменный объект из снимка собирается только через
// RehydrateDeal/RehydrateCRMSync, которые проверяют инварианты и не эмитят
// события: восстановление не является новым фактом домена.

// StageTransitionSnapshot — сохранённая запись перехода этапа.
type StageTransitionSnapshot struct {
	From   DealStage
	To     DealStage
	Actor  ActorID
	At     time.Time
	Reason string
}

// DealSnapshot — полное сохранённое состояние aggregate root deal.
type DealSnapshot struct {
	ID          DealID
	TenantID    TenantID
	CompanyRef  CompanyRef
	Stage       DealStage
	External    ExternalVersionSnapshot
	Attribution *AttributionProofSnapshot
	Version     int
	Transitions []StageTransitionSnapshot
}

// Snapshot возвращает состояние сделки для сохранения.
func (d *Deal) Snapshot() DealSnapshot {
	transitions := make([]StageTransitionSnapshot, 0, len(d.transitions))
	for _, t := range d.transitions {
		transitions = append(transitions, StageTransitionSnapshot{
			From:   t.from,
			To:     t.to,
			Actor:  t.actor,
			At:     t.at,
			Reason: t.reason,
		})
	}
	var attribution *AttributionProofSnapshot
	if d.attribution != nil {
		snapshot := d.attribution.Snapshot()
		attribution = &snapshot
	}
	return DealSnapshot{
		ID:          d.id,
		TenantID:    d.tenantID,
		CompanyRef:  d.companyRef,
		Stage:       d.stage,
		External:    d.external.Snapshot(),
		Attribution: attribution,
		Version:     d.version,
		Transitions: transitions,
	}
}

// RehydrateDeal восстанавливает сделку из сохранённого состояния, проверяя
// инварианты: известность этапа, положительную версию, непустой журнал переходов
// и совпадение последнего перехода с текущим этапом. События не эмитятся.
func RehydrateDeal(s DealSnapshot) (*Deal, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := s.CompanyRef.validate(); err != nil {
		return nil, err
	}
	if err := s.Stage.validate(); err != nil {
		return nil, err
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: сделка %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	external, err := rehydrateExternalVersion(s.External)
	if err != nil {
		return nil, fmt.Errorf("%w: сделка %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	var attribution *AttributionProof
	if s.Attribution != nil {
		proof, err := rehydrateAttributionProof(*s.Attribution)
		if err != nil {
			return nil, fmt.Errorf("%w: сделка %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		attribution = &proof
	}
	if len(s.Transitions) == 0 {
		return nil, fmt.Errorf("%w: сделка %s без журнала переходов", ErrInvalidSnapshot, s.ID)
	}
	transitions := make([]StageTransition, 0, len(s.Transitions))
	var last DealStage
	for _, ts := range s.Transitions {
		if !ts.To.IsValid() {
			return nil, fmt.Errorf("%w: сделка %s: неизвестный этап перехода %q", ErrInvalidSnapshot, s.ID, ts.To)
		}
		if ts.At.IsZero() {
			return nil, fmt.Errorf("%w: сделка %s: переход без времени", ErrInvalidSnapshot, s.ID)
		}
		transitions = append(transitions, StageTransition{
			from:   ts.From,
			to:     ts.To,
			actor:  ts.Actor,
			at:     ts.At,
			reason: ts.Reason,
		})
		last = ts.To
	}
	if last != s.Stage {
		return nil, fmt.Errorf(
			"%w: сделка %s: последний переход %q не совпадает с этапом %q",
			ErrInvalidSnapshot, s.ID, last, s.Stage,
		)
	}
	return &Deal{
		id:          s.ID,
		tenantID:    s.TenantID,
		companyRef:  s.CompanyRef,
		stage:       s.Stage,
		external:    external,
		attribution: attribution,
		transitions: transitions,
		version:     s.Version,
	}, nil
}

// CRMSyncSnapshot — полное сохранённое состояние aggregate root crm-sync.
type CRMSyncSnapshot struct {
	ID        CRMSyncID
	TenantID  TenantID
	DealID    DealID
	Status    SyncStatus
	External  ExternalVersionSnapshot
	UpdatedAt time.Time
	Revision  int
}

// Snapshot возвращает состояние синхронизации для сохранения.
func (s *CRMSync) Snapshot() CRMSyncSnapshot {
	return CRMSyncSnapshot{
		ID:        s.id,
		TenantID:  s.tenantID,
		DealID:    s.dealID,
		Status:    s.status,
		External:  s.version.Snapshot(),
		UpdatedAt: s.updatedAt,
		Revision:  s.revision,
	}
}

// RehydrateCRMSync восстанавливает запись синхронизации из сохранённого
// состояния, проверяя инварианты. События не эмитятся.
func RehydrateCRMSync(s CRMSyncSnapshot) (*CRMSync, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := s.DealID.validate(); err != nil {
		return nil, err
	}
	if err := s.Status.validate(); err != nil {
		return nil, fmt.Errorf("%w: синхронизация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if s.Revision <= 0 {
		return nil, fmt.Errorf("%w: синхронизация %s ревизии %d", ErrInvalidSnapshot, s.ID, s.Revision)
	}
	if s.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: синхронизация %s без времени", ErrInvalidSnapshot, s.ID)
	}
	external, err := rehydrateExternalVersion(s.External)
	if err != nil {
		return nil, fmt.Errorf("%w: синхронизация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if external.IsZero() && s.Status != SyncPending {
		return nil, fmt.Errorf("%w: синхронизация %s в статусе %s без внешней версии", ErrInvalidSnapshot, s.ID, s.Status)
	}
	return &CRMSync{
		id:        s.ID,
		tenantID:  s.TenantID,
		dealID:    s.DealID,
		status:    s.Status,
		version:   external,
		updatedAt: s.UpdatedAt,
		revision:  s.Revision,
	}, nil
}
