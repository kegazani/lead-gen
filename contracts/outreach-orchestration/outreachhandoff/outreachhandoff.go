// Package outreachhandoff — опубликованный контракт outreach-handoff контекста
// outreach-orchestration: приём передачи сгенерированных писем заявки из
// request-fulfillment для постановки в очередь утверждения оператором.
//
// Владелец контракта — outreach-orchestration (architecture/domain-catalog.yaml):
// он принимает команду и создаёт одну кампанию на заявку с черновиками, но не
// отправляет письма: отправка остаётся за утверждением человеком. Инициатор —
// request-fulfillment: он владеет сгенерированными письмами и наблюдаемым
// недобором. Контракт несёт только непрозрачные ссылки на получателя и материал,
// без персональных данных и сырых карточек.
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель зависит от контракта, не видя реализации.
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой (InProc),
// при разделении сервисов — сетевой клиент (httpport); выбирает точка сборки по
// топологии.
package outreachhandoff

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "outreach-handoff"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.2.0"

// ErrRejected — команда передачи отклонена по доменной причине потребителя
// (пустой тенант, отсутствующий или непригодный состав, нарушенная форма).
// Владелец различает этот отказ от сбоя транспорта и не выдаёт его за успешный
// приём.
var ErrRejected = errors.New("outreachhandoff: команда передачи отклонена потребителем")

// ErrUnavailable — потребитель передачи недоступен: контракт не подключён, сбой
// транспорта или сбой создания кампании. Владелец не выдаёт это за успешную
// передачу и не подменяет заглушкой.
var ErrUnavailable = errors.New("outreachhandoff: получатель передачи недоступен")

// Item — одно письмо передачи: непрозрачные ссылки без персональных данных.
type Item struct {
	// LeadID — идентификатор лида (нормализованный домен компании). Обязателен:
	// связывает черновик с найденной компанией.
	LeadID string
	// RecipientRef — непрозрачная ссылка на получателя (идентификатор контакта с
	// пригодным адресом). Обязательна: без получателя письмо не передаётся.
	RecipientRef string
	// MaterialRef — непрозрачная ссылка на материал письма (`asset_id@version_id`),
	// из которого разрешаются тема и тело. Обязательна.
	MaterialRef string
}

// Command — команда передачи писем заявки: одна кампания на заявку. `RequestID`
// служит ключом идемпотентности: повтор не создаёт вторую кампанию.
type Command struct {
	// RequestID — заявка, к которой относится передача. Обязателен.
	RequestID string
	// TenantID — тенант-владелец заявки и кампании. Обязателен.
	TenantID string
	// Actor — инициатор передачи (оператор/исполнение заявки). Может быть пустым.
	Actor string
	// Name — человекочитаемое имя кампании. Пусто — имя выводит потребитель.
	Name string
	// ExecutionPurpose отделяет редакторскую передачу от delivery. Пустое
	// значение сохраняет legacy delivery-семантику.
	ExecutionPurpose string `json:"execution_purpose,omitempty"`
	// Items — состав передачи: по одному письму на лид. Пустой состав не создаёт
	// кампанию и не подменяется фиктивным письмом.
	Items []Item
}

const (
	ExecutionPurposeDelivery  = "delivery"
	ExecutionPurposeEditorial = "editorial"
)

// Validate проверяет обязательные поля команды и состав, называя все нарушения
// разом. Пустой состав допустим как вход (потребитель не создаёт кампанию), но
// проверяется вызывающим явно.
func (c Command) Validate() error {
	var invalid []string
	if c.ExecutionPurpose != "" && c.ExecutionPurpose != ExecutionPurposeDelivery && c.ExecutionPurpose != ExecutionPurposeEditorial {
		invalid = append(invalid, "execution_purpose")
	}
	if strings.TrimSpace(c.RequestID) == "" {
		invalid = append(invalid, "request_id")
	}
	if strings.TrimSpace(c.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	for i, item := range c.Items {
		if strings.TrimSpace(item.LeadID) == "" {
			invalid = append(invalid, fmt.Sprintf("items[%d].lead_id", i))
		}
		if strings.TrimSpace(item.RecipientRef) == "" && c.ExecutionPurpose != ExecutionPurposeEditorial {
			invalid = append(invalid, fmt.Sprintf("items[%d].recipient_ref", i))
		}
		if strings.TrimSpace(item.MaterialRef) == "" {
			invalid = append(invalid, fmt.Sprintf("items[%d].material_ref", i))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// Result — наблюдаемый исход передачи.
type Result struct {
	// CampaignID — идентификатор кампании (детерминирован из RequestID).
	CampaignID string
	// Accepted — число принятых писем.
	Accepted int
	// Skipped — число пропущенных писем (например, непригодных).
	Skipped int
	// Repeated — признак повторного приёма: команда с этим RequestID уже была
	// обработана, новая кампания не создавалась.
	Repeated bool
}

// RequestCancellation identifies a request whose handoff/approval actions must
// be fenced by the owner. Replays are idempotent; cancellation is retained even
// when the campaign has not arrived yet.
type RequestCancellation struct {
	TenantID  string
	RequestID string
}

func (c RequestCancellation) Validate() error {
	var invalid []string
	if strings.TrimSpace(c.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(c.RequestID) == "" {
		invalid = append(invalid, "request_id")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// Port — команда передачи потребителю. ErrRejected — доменный отказ,
// ErrUnavailable — сбой транспорта или создание кампании.
type Port interface {
	Submit(ctx context.Context, command Command) (Result, error)
	// CancelRequest durably fences a request against later handoff, approval and
	// release. It does not retract an already-started external delivery.
	CancelRequest(ctx context.Context, cancellation RequestCancellation) error
}

// InProcHandler — функция прямого вызова потребителя для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// SubmitFunc реализует приём передачи.
	SubmitFunc func(ctx context.Context, command Command) (Result, error)
	// CancelFunc persists the request cancellation fence at the owner.
	CancelFunc func(ctx context.Context, cancellation RequestCancellation) error
}

// Submit реализует Port. Незаданная функция честно сообщает о недоступности
// потребителя, а не выдумывает успешную передачу.
func (h InProcHandler) Submit(ctx context.Context, command Command) (Result, error) {
	if h.SubmitFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.SubmitFunc(ctx, command)
}

// CancelRequest implements Port.
func (h InProcHandler) CancelRequest(ctx context.Context, cancellation RequestCancellation) error {
	if err := cancellation.Validate(); err != nil {
		return err
	}
	if h.CancelFunc == nil {
		return ErrUnavailable
	}
	return h.CancelFunc(ctx, cancellation)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
