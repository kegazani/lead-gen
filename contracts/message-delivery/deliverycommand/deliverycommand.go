package deliverycommand

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml).
const ContractName = "delivery-command"

// SchemaVersion — текущая версия схемы контракта (semver). Версия 1.1.0
// добавлена по правилу MINOR: тема и тело письма — необязательные поля, старые
// потребители продолжают работать без изменений. Версия 1.2.0 добавлена по тому
// же правилу: `campaign_id` — необязательная ссылка на кампанию-владельца.
const SchemaVersion = "1.2.0"

// ErrRejected — доставка отклонена владельцем по доменной причине (например,
// приостановлена репутация домена или получатель в стоп-листе). Потребитель
// различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("deliverycommand: доставка отклонена владельцем")

// ErrUnavailable — владелец доставки недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает исход.
var ErrUnavailable = errors.New("deliverycommand: владелец доставки недоступен")

// ChannelEmailName — единственный канал, который обслуживает контекст
// (capability outreach-channel-email). Набор закрыт.
const ChannelEmailName = "email"

// Status — нормализованный исход доставки в ответе команды.
type Status string

const (
	// StatusDelivered — сообщение принято провайдером.
	StatusDelivered Status = "delivered"
	// StatusFailed — доставка не состоялась.
	StatusFailed Status = "failed"
)

// Command — вход команды доставки. Идентификаторы, непрозрачные ссылки,
// проверенный адрес назначения и — с версии 1.1.0 — необязательное содержимое
// письма (тема и тело), необходимое реальному провайдеру отправки.
type Command struct {
	// CommandID — идентификатор команды. Вместе с тенантом образует ключ
	// идемпотентности повторной команды.
	CommandID string
	// TenantID — тенант-владелец сообщения.
	TenantID string
	// ApprovedActionID — утверждённое действие, санкционировавшее отправку.
	// Обязателен: сообщение создаётся только для утверждённого действия.
	ApprovedActionID string
	// CampaignID — непрозрачная ссылка на кампанию-владельца, унаследованная от
	// утверждённого действия (его correlation_id равен идентификатору кампании).
	// Необязательное поле версии 1.2.0.
	CampaignID string
	// RecipientRef — непрозрачная ссылка на получателя (не адрес).
	RecipientRef string
	// DeliveryAddress — проверенный адрес назначения. Единственное поле с
	// адресом: без него отправка невозможна.
	DeliveryAddress string
	// SenderDomain — отправляющий домен, репутация которого учитывается.
	SenderDomain string
	// Subject — тема письма. Необязательна (аддитивное поле версии 1.1.0):
	// источник содержимого остаётся за вызывающим.
	Subject string
	// Body — тело письма. Необязательно (аддитивное поле версии 1.1.0):
	// заполняется, когда вызывающий передаёт готовое содержимое.
	Body string
	// Channel — канал исполнения. Для этого контекста допустим только email.
	Channel string
	// CorrelationID — сквозной сценарий (утверждённое действие).
	CorrelationID string
	// CausationID — идентификатор сообщения-причины (решение оператора).
	CausationID string
}

// Validate проверяет обязательные поля команды и собирает все отсутствующие или
// некорректные, а не возвращает ошибку по первому. Канал сверяется с закрытым
// набором.
func (c Command) Validate() error {
	var invalid []string
	if strings.TrimSpace(c.CommandID) == "" {
		invalid = append(invalid, "command_id")
	}
	if strings.TrimSpace(c.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(c.ApprovedActionID) == "" {
		invalid = append(invalid, "approved_action_id")
	}
	if strings.TrimSpace(c.RecipientRef) == "" {
		invalid = append(invalid, "recipient_ref")
	}
	if strings.TrimSpace(c.DeliveryAddress) == "" {
		invalid = append(invalid, "delivery_address")
	}
	if strings.TrimSpace(c.SenderDomain) == "" {
		invalid = append(invalid, "sender_domain")
	}
	if strings.ToLower(strings.TrimSpace(c.Channel)) != ChannelEmailName {
		invalid = append(invalid, "channel")
	}
	if strings.TrimSpace(c.CorrelationID) == "" {
		invalid = append(invalid, "correlation_id")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("deliverycommand: delivery-command: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности команды. Ключ — (tenant_id,
// approved_action_id): повторная отправка того же утверждённого действия не
// создаёт второго внешнего эффекта. Идентификатор утверждённого действия
// уникален в рамках кампании-владельца, поэтому действия разных кампаний к одному
// получателю дают разные ключи. Компоненты кодируются, чтобы разделитель не
// допускал коллизий.
func (c Command) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s", encodeKeyPart(c.TenantID), encodeKeyPart(c.ApprovedActionID))
}

// Result — результат команды доставки.
type Result struct {
	// MessageID — идентификатор сообщения во владении message-delivery.
	MessageID string
	// Status — нормализованный исход доставки.
	Status Status
	// AttemptNumber — номер попытки, к которой относится исход.
	AttemptNumber int
	// Recorded равно false, если команда повторена с тем же ключом
	// идемпотентности: повтор не создаёт второго сообщения и второго эффекта.
	Recorded bool
}

// Port — команда доставки владельцу. Реализация создаёт сообщение и выполняет
// отправку; ErrRejected и ErrUnavailable — доменные исходы и сбой транспорта.
type Port interface {
	Deliver(ctx context.Context, cmd Command) (Result, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// DeliverFunc реализует команду доставки.
	DeliverFunc func(ctx context.Context, cmd Command) (Result, error)
}

// Deliver реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает исход.
func (h InProcHandler) Deliver(ctx context.Context, cmd Command) (Result, error) {
	if h.DeliverFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.DeliverFunc(ctx, cmd)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }

// encodeKeyPart кодирует компонент ключа идемпотентности. Совпадает с кодированием
// конверта контрактов контекста, поэтому ключ, вычисленный в разных местах,
// совпадает.
func encodeKeyPart(part string) string {
	return url.QueryEscape(part)
}
