package domain

import (
	"fmt"
	"time"
)

// SyncStatus — состояние синхронизации с внешней CRM.
type SyncStatus string

const (
	// SyncPending — синхронизация ещё не применяла внешнюю версию.
	SyncPending SyncStatus = "pending"

	// SyncSynced — последняя внешняя версия применена.
	SyncSynced SyncStatus = "synced"

	// SyncConflict — обнаружен конфликт версий: локальное состояние не
	// перезаписано.
	SyncConflict SyncStatus = "conflict"
)

// IsValid сообщает, что состояние объявлено доменом.
func (s SyncStatus) IsValid() bool {
	switch s {
	case SyncPending, SyncSynced, SyncConflict:
		return true
	default:
		return false
	}
}

// validate отвергает состояние вне согласованного набора.
func (s SyncStatus) validate() error {
	if !s.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidSnapshot, s)
	}
	return nil
}

// SyncInput — данные сверки с внешней CRM.
type SyncInput struct {
	// At — время наблюдения внешнего состояния.
	At time.Time

	// EventID — идентификатор порождаемого события конфликта.
	EventID EventID

	// CorrelationID — идентификатор сквозного сценария.
	CorrelationID CorrelationID

	// CausationID — идентификатор события-причины.
	CausationID CausationID
}

// validate проверяет обязательные поля сверки.
func (in SyncInput) validate() error {
	if in.At.IsZero() {
		return ErrMissingTime
	}
	if string(in.EventID) == "" {
		return fmt.Errorf("%w: событие синхронизации", ErrMissingEventID)
	}
	return nil
}

// NewSyncParams — данные создания записи синхронизации.
type NewSyncParams struct {
	ID       CRMSyncID
	TenantID TenantID
	DealID   DealID

	// External — внешняя версия, с которой стартует синхронизация. Нулевое
	// значение означает, что внешняя версия ещё не применена (status pending).
	External ExternalVersion

	At time.Time
}

// SyncResult — результат сверки внешней версии.
type SyncResult struct {
	// Applied — была ли внешняя версия применена к локальному состоянию.
	Applied bool

	// Status — состояние синхронизации после сверки.
	Status SyncStatus

	// External — применённая внешняя версия (не изменяется при конфликте).
	External ExternalVersion
}

// CRMSync — aggregate root crm-sync: синхронизация сделки с внешней CRM.
//
// Агрегат хранит внешнюю версию записи CRM и обеспечивает инвариант
// architecture/domain-catalog.yaml: синхронизация хранит внешнюю версию и не
// перезаписывает локальное состояние при конфликте. Решение о сверке принимает
// политика conflict-resolution; агрегат применяет её исход.
type CRMSync struct {
	id        CRMSyncID
	tenantID  TenantID
	dealID    DealID
	status    SyncStatus
	version   ExternalVersion
	updatedAt time.Time

	revision int
	events   []DomainEvent
}

// NewCRMSync создаёт запись синхронизации сделки. Если внешняя версия задана,
// синхронизация считается состоявшейся (synced), иначе — ожидающей (pending).
func NewCRMSync(params NewSyncParams) (*CRMSync, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.DealID.validate(); err != nil {
		return nil, err
	}
	if params.At.IsZero() {
		return nil, ErrMissingTime
	}
	status := SyncPending
	if !params.External.IsZero() {
		status = SyncSynced
	}
	return &CRMSync{
		id:        params.ID,
		tenantID:  params.TenantID,
		dealID:    params.DealID,
		status:    status,
		version:   params.External,
		updatedAt: params.At,
		revision:  1,
	}, nil
}

// ID возвращает идентификатор записи синхронизации.
func (s *CRMSync) ID() CRMSyncID { return s.id }

// TenantID возвращает тенанта-владельца записи.
func (s *CRMSync) TenantID() TenantID { return s.tenantID }

// DealID возвращает сделку, к которой относится синхронизация.
func (s *CRMSync) DealID() DealID { return s.dealID }

// Status возвращает состояние синхронизации.
func (s *CRMSync) Status() SyncStatus { return s.status }

// Version возвращает применённую внешнюю версию.
func (s *CRMSync) Version() ExternalVersion { return s.version }

// UpdatedAt возвращает время последней сверки.
func (s *CRMSync) UpdatedAt() time.Time { return s.updatedAt }

// Revision возвращает локальную версию агрегата — токен оптимистичной блокировки.
func (s *CRMSync) Revision() int { return s.revision }

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (s *CRMSync) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), s.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (s *CRMSync) PullDomainEvents() []DomainEvent {
	if len(s.events) == 0 {
		return nil
	}
	out := s.events
	s.events = nil
	return out
}

// RecordExternalVersion сверяет наблюдаемую внешнюю версию с локальной.
//
// Применимая версия фиксируется как применённая (synced). Устаревшая версия не
// вносит изменений. Конфликт не перезаписывает локальное состояние: применённая
// версия сохраняется, состояние переходит в conflict, и эмитится событие
// crm-sync-conflict.
func (s *CRMSync) RecordExternalVersion(in SyncInput, external ExternalVersion) (SyncResult, error) {
	if err := in.validate(); err != nil {
		return SyncResult{}, err
	}
	if external.IsZero() {
		return SyncResult{}, fmt.Errorf("%w: пустая внешняя версия", ErrInvalidExternalVersion)
	}
	decision := ConflictResolution(s.version, external)
	switch decision.Outcome {
	case ConflictApply:
		s.version = external
		s.status = SyncSynced
		s.updatedAt = in.At
		s.revision++
		return SyncResult{Applied: true, Status: s.status, External: s.version}, nil
	case ConflictStale:
		return SyncResult{Applied: false, Status: s.status, External: s.version}, nil
	default:
		s.status = SyncConflict
		s.updatedAt = in.At
		s.revision++
		s.emit(CRMSyncConflict{
			EventHeader: EventHeader{
				EventID:       in.EventID,
				OccurredAt:    in.At,
				TenantID:      s.tenantID,
				CorrelationID: in.CorrelationID,
				CausationID:   in.CausationID,
			},
			SyncID:           s.id,
			DealID:           s.dealID,
			LocalRef:         s.version.Ref(),
			LocalRevision:    s.version.Revision(),
			ExternalRef:      external.Ref(),
			ExternalRevision: external.Revision(),
			Reason:           decision.Reason,
		})
		return SyncResult{Applied: false, Status: s.status, External: s.version}, nil
	}
}

// emit записывает события в буфер агрегата.
func (s *CRMSync) emit(events ...DomainEvent) {
	s.events = append(s.events, events...)
}
