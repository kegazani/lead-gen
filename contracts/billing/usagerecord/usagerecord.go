package usagerecord

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml, contracts billing).
const ContractName = "usage-record"

// SchemaVersion — текущая версия схемы контракта (semver). MAJOR несовместимо,
// MINOR добавляет опциональное поле, PATCH уточняет смысл без изменения состава.
const SchemaVersion = "1.0.0"

var (
	// ErrRejected — запись потребления отклонена владельцем по доменной причине
	// (возможность не входит в тариф, превышен лимит). Потребитель различает этот
	// отказ от сбоя транспорта.
	ErrRejected = errors.New("usagerecord: запись потребления отклонена владельцем")

	// ErrUnavailable — владелец записей потребления недоступен: контракт не
	// подключён или сбой транспорта. Потребитель не выдаёт это за успех.
	ErrUnavailable = errors.New("usagerecord: владелец записей потребления недоступен")
)

// Envelope — общий конверт контракта. Поля позволяют потребителю дедуплицировать
// сообщения и выстраивать причинно-следственные связи (correlation_id /
// causation_id). Версионирование схемы — semver.
type Envelope struct {
	// EventID — стабильный идентификатор сообщения; используется для дедупликации.
	EventID string `json:"event_id"`
	// OccurredAt — момент возникновения факта в UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// TenantID — граница изоляции тенанта.
	TenantID string `json:"tenant_id"`
	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string `json:"correlation_id"`
	// CausationID — идентификатор сообщения-причины; у корневого сообщения пуст.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (usage-record).
	Name string `json:"name"`
	// SchemaVersion — версия схемы контракта (semver).
	SchemaVersion string `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие,
// а не возвращает ошибку по первому. CausationID допускается пустым.
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
		return fmt.Errorf("usagerecord: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// UsageRecord — запись потребления возможности тарифа за период. Контракт несёт
// только идентификаторы и объём: ни персональных данных, ни сумм начисления.
//
// Ключ идемпотентности — (PeriodKey, UsageKey): повторная доставка той же записи
// не создаёт второго эффекта и не расходует лимит дважды.
type UsageRecord struct {
	Envelope

	// RecordID — идентификатор записи потребления.
	RecordID string `json:"record_id"`
	// Feature — возможность тарифа, к которой относится потребление.
	Feature string `json:"feature"`
	// Quantity — положительный объём потребления в единицах возможности.
	Quantity int64 `json:"quantity"`
	// PeriodKey — ключ тарифного периода (интервал начисления).
	PeriodKey string `json:"period_key"`
	// UsageKey — ключ записи внутри периода; вместе с PeriodKey задаёт
	// идемпотентность.
	UsageKey string `json:"usage_key"`
}

// Validate проверяет конверт и обязательные поля записи. Имя контракта сверяется
// с каноническим.
func (r UsageRecord) Validate() error {
	if err := r.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if r.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(r.RecordID) == "" {
		invalid = append(invalid, "record_id")
	}
	if strings.TrimSpace(r.Feature) == "" {
		invalid = append(invalid, "feature")
	}
	if r.Quantity <= 0 {
		invalid = append(invalid, "quantity")
	}
	if strings.TrimSpace(r.PeriodKey) == "" {
		invalid = append(invalid, "period_key")
	}
	if strings.TrimSpace(r.UsageKey) == "" {
		invalid = append(invalid, "usage_key")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("usagerecord: usage-record: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности записи. Компоненты кодируются,
// чтобы разделитель не допускал коллизий между разными парами.
func (r UsageRecord) IdempotencyKey() string {
	return encodeKeyPart(r.PeriodKey) + "|" + encodeKeyPart(r.UsageKey)
}

// Ack — подтверждение приёма записи потребления. Duplicate=true означает, что
// запись уже была учтена ранее и повторного расхода лимита не произошло.
type Ack struct {
	// Accepted — запись принята (в том числе как повтор).
	Accepted bool `json:"accepted"`
	// Duplicate — запись распознана как повторная по ключу идемпотентности.
	Duplicate bool `json:"duplicate"`
	// Remaining — остаток лимита возможности после учёта.
	Remaining int64 `json:"remaining"`
	// InvoiceIssued — по итогам записи выставлен счёт за период.
	InvoiceIssued bool `json:"invoice_issued"`
	// InvoiceID — идентификатор выставленного счёта, если он был выставлен.
	InvoiceID string `json:"invoice_id,omitempty"`
}

// Port — приём записи потребления владельцем тарификации. Реализация учитывает
// запись идемпотентно и возвращает остаток лимита; ErrRejected — доменный отказ,
// ErrUnavailable — отсутствие контракта или сбой транспорта.
type Port interface {
	Record(ctx context.Context, record UsageRecord) (Ack, error)
}

// Recorder — владельческая сторона контракта: реализация, которую связывает
// in-process адаптер. Отдельное имя отделяет намерение владельца от интерфейса
// потребителя.
type Recorder interface {
	Record(ctx context.Context, record UsageRecord) (Ack, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// RecordFunc реализует приём записи потребления.
	RecordFunc func(ctx context.Context, record UsageRecord) (Ack, error)
}

// Record реализует Port. Незаданная функция честно сообщает о недоступности.
func (h InProcHandler) Record(ctx context.Context, record UsageRecord) (Ack, error) {
	if h.RecordFunc == nil {
		return Ack{}, ErrUnavailable
	}
	return h.RecordFunc(ctx, record)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }

// encodeKeyPart кодирует компонент ключа идемпотентности.
func encodeKeyPart(part string) string { return url.QueryEscape(part) }
