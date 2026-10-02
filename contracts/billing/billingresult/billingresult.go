package billingresult

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml, contracts billing).
const ContractName = "billing-result"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

var (
	// ErrRejected — результат отклонён владельцем (некорректный контракт).
	ErrRejected = errors.New("billingresult: результат биллинга отклонён владельцем")

	// ErrUnavailable — получатель результата недоступен: контракт не подключён
	// или сбой транспорта.
	ErrUnavailable = errors.New("billingresult: получатель результата недоступен")
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
		return fmt.Errorf("billingresult: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Kind — вид результата биллинга.
type Kind string

const (
	// KindCharge — начисление по подписке (счёт за период).
	KindCharge Kind = "charge"
	// KindSuccessFee — вознаграждение за подтверждённый результат.
	KindSuccessFee Kind = "success-fee"
)

// IsValid сообщает, что вид результата объявлен контрактом.
func (k Kind) IsValid() bool {
	return k == KindCharge || k == KindSuccessFee
}

// Result — результат биллинга: начисление по подписке или вознаграждение за
// подтверждённый результат. Контракт передаёт сумму в минимальных единицах
// валюты и непрозрачную ссылку-источник: персональных данных здесь нет.
type Result struct {
	Envelope

	// ResultID — идентификатор результата.
	ResultID string `json:"result_id"`
	// Kind — вид результата.
	Kind Kind `json:"kind"`
	// SubscriptionID — подписка, к которой отнесён результат.
	SubscriptionID string `json:"subscription_id"`
	// SourceRef — непрозрачная ссылка на источник (счёт, подтверждённый
	// результат). Не персональные данные.
	SourceRef string `json:"source_ref"`
	// AmountMinor — сумма в минимальных единицах валюты.
	AmountMinor int64 `json:"amount_minor"`
	// Currency — код валюты (ISO 4217, верхний регистр).
	Currency string `json:"currency"`
	// CalculationVersion — версия правила расчёта вознаграждения; заполняется
	// только для KindSuccessFee.
	CalculationVersion string `json:"calculation_version,omitempty"`
}

// Validate проверяет конверт и обязательные поля результата.
func (r Result) Validate() error {
	if err := r.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if r.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(r.ResultID) == "" {
		invalid = append(invalid, "result_id")
	}
	if !r.Kind.IsValid() {
		invalid = append(invalid, "kind")
	}
	if strings.TrimSpace(r.SubscriptionID) == "" {
		invalid = append(invalid, "subscription_id")
	}
	if strings.TrimSpace(r.SourceRef) == "" {
		invalid = append(invalid, "source_ref")
	}
	if r.AmountMinor < 0 {
		invalid = append(invalid, "amount_minor")
	}
	if len(strings.TrimSpace(r.Currency)) != 3 {
		invalid = append(invalid, "currency")
	}
	if r.Kind == KindSuccessFee && strings.TrimSpace(r.CalculationVersion) == "" {
		invalid = append(invalid, "calculation_version")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("billingresult: billing-result: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IsSuccessFee сообщает, что результат — вознаграждение за подтверждённый
// результат.
func (r Result) IsSuccessFee() bool { return r.Kind == KindSuccessFee }

// Publisher — владельческая сторона контракта: публикация результата биллинга
// получателю. Невалидный контракт не публикуется.
type Publisher interface {
	Publish(ctx context.Context, result Result) error
}

// Subscriber — сторона получателя результата биллинга.
type Subscriber interface {
	Receive(ctx context.Context, result Result) error
}

// InProcSubscriber — функция-получатель для co-location и тестов.
type InProcSubscriber struct {
	// ReceiveFunc реализует приём результата.
	ReceiveFunc func(ctx context.Context, result Result) error
}

// Receive реализует Subscriber. Незаданная функция честно сообщает о
// недоступности получателя.
func (s InProcSubscriber) Receive(ctx context.Context, result Result) error {
	if s.ReceiveFunc == nil {
		return ErrUnavailable
	}
	return s.ReceiveFunc(ctx, result)
}

// InProc заворачивает получателя в Publisher для прямого вызова в одном
// процессе: публикация валидируется и передаётся получателю. Сетевой адаптер при
// разделении сервисов даёт ту же форму.
func InProc(subscriber Subscriber) Publisher { return inProcPublisher{subscriber: subscriber} }

// inProcPublisher — прямая передача результата получателю.
type inProcPublisher struct {
	subscriber Subscriber
}

// Publish реализует Publisher.
func (p inProcPublisher) Publish(ctx context.Context, result Result) error {
	if p.subscriber == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	return p.subscriber.Receive(ctx, result)
}
