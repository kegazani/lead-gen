package domain

import (
	"fmt"
	"time"
)

// Сохранённое состояние агрегатов и его восстановление.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегатов, поэтому он
// читает и пишет экспортированные снимки. Доменный объект из снимка собирается
// только через Rehydrate*, который проверяет инварианты и не эмитит события:
// восстановление не является новым фактом домена.

// AdapterRegistrationSnapshot — сохранённое состояние регистрации адаптера.
type AdapterRegistrationSnapshot struct {
	ID       AdapterID
	Manifest Manifest
}

// Snapshot возвращает состояние регистрации для сохранения.
func (r *AdapterRegistration) Snapshot() AdapterRegistrationSnapshot {
	return AdapterRegistrationSnapshot{ID: r.id, Manifest: r.manifest}
}

// RehydrateAdapterRegistration восстанавливает регистрацию из снимка.
//
// Проверяются идентификатор и manifest (имя, версия, набор возможностей);
// противоречивый снимок отвергается доменной ошибкой. События не эмитятся.
func RehydrateAdapterRegistration(s AdapterRegistrationSnapshot) (*AdapterRegistration, error) {
	if err := s.ID.validate(); err != nil {
		return nil, fmt.Errorf("%w: регистрация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if s.Manifest.IsZero() {
		return nil, fmt.Errorf("%w: регистрация %s: manifest пуст", ErrInvalidSnapshot, s.ID)
	}
	if err := s.Manifest.validate(); err != nil {
		return nil, fmt.Errorf("%w: регистрация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	return &AdapterRegistration{id: s.ID, manifest: s.Manifest}, nil
}

// IntegrationInvocationSnapshot — сохранённое состояние вызова интеграции.
// ExternalRef хранится копией: привязка внешнего идентификатора отдельна от
// внутреннего идентификатора вызова.
type IntegrationInvocationSnapshot struct {
	ID              InvocationID
	TenantID        TenantID
	CorrelationRef  CorrelationRef
	IntegrationType IntegrationType
	AdapterName     AdapterName
	Operation       Operation
	CommandKey      CommandKey
	State           InvocationState
	Category        ErrorCategory
	ExternalRef     *ExternalRef
	StartedAt       time.Time
	FinishedAt      time.Time
}

// Snapshot возвращает состояние вызова для сохранения.
func (i *IntegrationInvocation) Snapshot() IntegrationInvocationSnapshot {
	snapshot := IntegrationInvocationSnapshot{
		ID:              i.id,
		TenantID:        i.tenantID,
		CorrelationRef:  i.correlationRef,
		IntegrationType: i.integrationType,
		AdapterName:     i.adapterName,
		Operation:       i.operation,
		CommandKey:      i.commandKey,
		State:           i.state,
		Category:        i.category,
		StartedAt:       i.startedAt,
		FinishedAt:      i.finishedAt,
	}
	if i.externalRef != nil {
		ref := *i.externalRef
		snapshot.ExternalRef = &ref
	}
	return snapshot
}

// RehydrateIntegrationInvocation восстанавливает вызов из снимка.
//
// Восстановление проверяет согласованность состояния вызова с его результатом:
// успех обязан нести привязку внешнего идентификатора и не нести категории;
// ошибка и тайм-аут — нести категорию и не нести привязки; отмена — ни того, ни
// другого. Противоречивый снимок отвергается доменной ошибкой. События не
// эмитятся.
func RehydrateIntegrationInvocation(s IntegrationInvocationSnapshot) (*IntegrationInvocation, error) {
	if err := s.ID.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.CorrelationRef.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.IntegrationType.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.AdapterName.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.Operation.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if err := s.CommandKey.validate(); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	if !s.State.IsValid() {
		return nil, wrapInvalidSnapshot(s.ID, fmt.Errorf("%w: %q", ErrInvalidInvocationState, s.State))
	}
	if s.StartedAt.IsZero() {
		return nil, wrapInvalidSnapshot(s.ID, fmt.Errorf("момент начала вызова не задан"))
	}

	invocation := &IntegrationInvocation{
		id:              s.ID,
		tenantID:        s.TenantID,
		correlationRef:  s.CorrelationRef,
		integrationType: s.IntegrationType,
		adapterName:     s.AdapterName,
		operation:       s.Operation,
		commandKey:      s.CommandKey,
		state:           s.State,
		startedAt:       s.StartedAt,
		finishedAt:      s.FinishedAt,
	}

	if err := rehydrateOutcome(invocation, s); err != nil {
		return nil, wrapInvalidSnapshot(s.ID, err)
	}
	return invocation, nil
}

// rehydrateOutcome восстанавливает исход вызова из снимка, проверяя его
// согласованность с состоянием.
func rehydrateOutcome(invocation *IntegrationInvocation, s IntegrationInvocationSnapshot) error {
	switch s.State {
	case InvocationPending:
		if !s.FinishedAt.IsZero() || s.Category != "" || s.ExternalRef != nil {
			return fmt.Errorf("начатый вызов %s несёт исход", s.ID)
		}
	case InvocationSucceeded:
		if s.ExternalRef != nil {
			if err := s.ExternalRef.validate(); err != nil {
				return err
			}
			if s.ExternalRef.IntegrationType != s.IntegrationType || s.ExternalRef.AdapterName != s.AdapterName {
				return fmt.Errorf("%w: привязка вызова %s", ErrInvalidExternalRef, s.ID)
			}
		}
		if s.Category != "" {
			return fmt.Errorf("успешный вызов %s несёт категорию ошибки", s.ID)
		}
		if s.FinishedAt.IsZero() {
			return fmt.Errorf("успешный вызов %s без момента завершения", s.ID)
		}
		if s.ExternalRef != nil {
			ref := *s.ExternalRef
			invocation.externalRef = &ref
		}
		invocation.finishedAt = s.FinishedAt
	case InvocationFailed:
		if !IsValidErrorCategory(s.Category) {
			return fmt.Errorf("ошибочный вызов %s с категорией %q", s.ID, s.Category)
		}
		if s.ExternalRef != nil {
			return fmt.Errorf("ошибочный вызов %s несёт привязку внешнего идентификатора", s.ID)
		}
		if s.FinishedAt.IsZero() {
			return fmt.Errorf("ошибочный вызов %s без момента завершения", s.ID)
		}
		invocation.category = s.Category
		invocation.finishedAt = s.FinishedAt
	case InvocationTimedOut:
		if !IsTemporaryCategory(s.Category) {
			return fmt.Errorf("тайм-аут вызова %s с категорией %q", s.ID, s.Category)
		}
		if s.ExternalRef != nil {
			return fmt.Errorf("тайм-аут вызова %s несёт привязку внешнего идентификатора", s.ID)
		}
		if s.FinishedAt.IsZero() {
			return fmt.Errorf("тайм-аут вызова %s без момента завершения", s.ID)
		}
		invocation.category = s.Category
		invocation.finishedAt = s.FinishedAt
	case InvocationCancelled:
		if s.Category != "" || s.ExternalRef != nil {
			return fmt.Errorf("отменённый вызов %s несёт исход", s.ID)
		}
		if s.FinishedAt.IsZero() {
			return fmt.Errorf("отменённый вызов %s без момента завершения", s.ID)
		}
		invocation.finishedAt = s.FinishedAt
	default:
		return fmt.Errorf("%w: %q", ErrInvalidInvocationState, s.State)
	}
	return nil
}

func wrapInvalidSnapshot(id InvocationID, err error) error {
	return fmt.Errorf("%w: вызов %s: %v", ErrInvalidSnapshot, id, err)
}
