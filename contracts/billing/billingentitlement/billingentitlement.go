package billingentitlement

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml, contracts billing).
const ContractName = "billing-entitlement"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

var (
	// ErrNotFound — подписка тенанта неизвестна владельцу. Существование чужой
	// подписки этим отказом не раскрывается.
	ErrNotFound = errors.New("billingentitlement: подписка не найдена")

	// ErrUnavailable — владелец прав и лимитов недоступен: контракт не подключён
	// или сбой транспорта. Потребитель не подставляет права по умолчанию.
	ErrUnavailable = errors.New("billingentitlement: владелец прав и лимитов недоступен")
)

// Envelope — общий конверт контракта.
type Envelope struct {
	EventID       string    `json:"event_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id,omitempty"`
	Name          string    `json:"name"`
	SchemaVersion string    `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие.
func (e Envelope) Validate() error {
	var invalid []string
	if strings.TrimSpace(e.EventID) == "" {
		invalid = append(invalid, "event_id")
	}
	if e.OccurredAt.IsZero() {
		invalid = append(invalid, "occurred_at")
	}
	if strings.TrimSpace(e.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(e.CorrelationID) == "" {
		invalid = append(invalid, "correlation_id")
	}
	if strings.TrimSpace(e.Name) == "" {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(e.SchemaVersion) == "" {
		invalid = append(invalid, "schema_version")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("billingentitlement: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Limit — тарифный лимит возможности в периоде: допустимый объём, потреблённый
// объём и остаток.
type Limit struct {
	// Feature — возможность тарифа.
	Feature string `json:"feature"`
	// Limit — допустимый объём за период.
	Limit int64 `json:"limit"`
	// Used — потреблённый объём за период.
	Used int64 `json:"used"`
	// Remaining — остаток лимита (Limit - Used, не меньше нуля).
	Remaining int64 `json:"remaining"`
}

// Entitlement — тарифные права и лимиты подписки тенанта за период. Отражает
// только возможности и объёмы: сумм и платёжных данных здесь нет.
type Entitlement struct {
	Envelope

	// SubscriptionID — идентификатор подписки.
	SubscriptionID string `json:"subscription_id"`
	// TariffID — идентификатор тарифа.
	TariffID string `json:"tariff_id"`
	// Status — состояние подписки (active/suspended). suspended означает
	// приостановку доступа с сохранением данных.
	Status string `json:"status"`
	// PeriodStart — начало тарифного периода (UTC).
	PeriodStart time.Time `json:"period_start"`
	// PeriodEnd — конец тарифного периода (UTC).
	PeriodEnd time.Time `json:"period_end"`
	// Limits — лимиты по возможностям тарифа.
	Limits []Limit `json:"limits"`
}

// Validate проверяет конверт и обязательные поля прав.
func (e Entitlement) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if e.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(e.SubscriptionID) == "" {
		invalid = append(invalid, "subscription_id")
	}
	if strings.TrimSpace(e.TariffID) == "" {
		invalid = append(invalid, "tariff_id")
	}
	if strings.TrimSpace(e.Status) == "" {
		invalid = append(invalid, "status")
	}
	if e.PeriodStart.IsZero() {
		invalid = append(invalid, "period_start")
	}
	if e.PeriodEnd.IsZero() {
		invalid = append(invalid, "period_end")
	}
	for i, limit := range e.Limits {
		if strings.TrimSpace(limit.Feature) == "" {
			invalid = append(invalid, fmt.Sprintf("limits[%d].feature", i))
		}
		if limit.Limit < 0 {
			invalid = append(invalid, fmt.Sprintf("limits[%d].limit", i))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("billingentitlement: billing-entitlement: некорректные или отсутствующие поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Allows сообщает, что возможность входит в тариф тенанта.
func (e Entitlement) Allows(feature string) bool {
	_, ok := e.Remaining(feature)
	return ok
}

// Remaining возвращает остаток лимита возможности и признак её наличия в тарифе.
func (e Entitlement) Remaining(feature string) (int64, bool) {
	for _, limit := range e.Limits {
		if limit.Feature == feature {
			return limit.Remaining, true
		}
	}
	return 0, false
}

// Port — чтение прав и лимитов тенанта. Реализация возвращает ErrNotFound, если
// подписка тенанта неизвестна; ErrUnavailable — отсутствие контракта или сбой
// транспорта. При разделении сервисов policy доставляется подпиской; in-process
// адаптер даёт ту же форму для co-location.
type Port interface {
	Entitlement(ctx context.Context, tenantID string) (Entitlement, error)
}

// Source — владельческая сторона контракта: реализация, которую связывает
// in-process адаптер.
type Source interface {
	Entitlement(ctx context.Context, tenantID string) (Entitlement, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// EntitlementFunc реализует чтение прав тенанта.
	EntitlementFunc func(ctx context.Context, tenantID string) (Entitlement, error)
}

// Entitlement реализует Port. Незаданная функция честно сообщает о недоступности.
func (h InProcHandler) Entitlement(ctx context.Context, tenantID string) (Entitlement, error) {
	if h.EntitlementFunc == nil {
		return Entitlement{}, ErrUnavailable
	}
	return h.EntitlementFunc(ctx, tenantID)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
