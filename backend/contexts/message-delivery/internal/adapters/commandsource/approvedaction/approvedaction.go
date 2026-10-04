package approvedaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	contracts "lead-gen/contracts/outreach-orchestration/approvedaction"
)

// defaultBuffer — ёмкость очереди команд по умолчанию.
const defaultBuffer = 64

// Ошибки моста. Повторное действие — не ошибка (идемпотентность); невалидное
// действие, не-email канал и неразрешимая ссылка — доменный отказ (ErrRejected);
// недоступность резолвера — честная недоступность (ErrUnavailable).
var (
	// ErrRejected — действие отклонено по доменной причине: невалидный контракт,
	// не-email канал или неразрешимая ссылка. Отказ виден вызывающему и не
	// маскируется под успех.
	ErrRejected = errors.New("approvedaction: действие отклонено")

	// ErrUnavailable — резолвер ссылок недоступен: контракт не подключён или сбой
	// транспорта. Адрес и материал не выдумываются.
	ErrUnavailable = errors.New("approvedaction: резолвер ссылок недоступен")
)

// systemClock — часы процесса по умолчанию для поля RequestedAt.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки моста.
type Config struct {
	// Recipients — порт разрешения ссылки получателя. Обязателен: без него
	// команду доставки собрать нельзя.
	Recipients ports.RecipientResolver

	// Materials — порт разрешения ссылки на версию материала. Необязателен, если
	// действия не несут MaterialVersionRef; при заданной ссылке обязателен.
	Materials ports.MaterialResolver

	// SenderDomain — отправляющий домен доставки. Обязателен.
	SenderDomain string

	// Clock — источник времени. nil → системные часы.
	Clock ports.Clock

	// Buffer — ёмкость очереди команд. Неположительное значение → значение по
	// умолчанию.
	Buffer int
}

// Result — исход приёма утверждённого действия.
type Result struct {
	// CommandID — идентификатор собранной команды доставки. Для повтора он тот
	// же, что у первой команды.
	CommandID domain.CommandID

	// Duplicate равно true, если действие уже было принято: вторая команда не
	// создаётся, повтор виден как повтор.
	Duplicate bool
}

// Adapter — мост утверждённого действия в источнике команд роли sender.
//
// Адаптер реализует ports.CommandSource (Next) и совместим с
// approvedaction.Publisher (Publish). Безопасен для конкурентного использования.
type Adapter struct {
	queue        *commandsource.Memory
	recipients   ports.RecipientResolver
	materials    ports.MaterialResolver
	senderDomain domain.SenderDomain
	clock        ports.Clock

	mu   sync.Mutex
	seen map[string]struct{}
}

// Адаптер реализует источник команд роли sender и потребительский интерфейс
// контракта approved-action.
var (
	_ ports.CommandSource = (*Adapter)(nil)
	_ contracts.Publisher = (*Adapter)(nil)
)

// New собирает мост. Отсутствие резолвера получателя или отправляющего домена —
// ошибка конфигурации, а не молчаливый отказ на пути доставки.
func New(cfg Config) (*Adapter, error) {
	if cfg.Recipients == nil {
		return nil, errors.New("approvedaction: не задан резолвер получателя")
	}
	senderDomain, err := domain.ParseSenderDomain(cfg.SenderDomain)
	if err != nil {
		return nil, fmt.Errorf("approvedaction: отправляющий домен: %w", err)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Adapter{
		queue:        commandsource.New(cfg.Buffer),
		recipients:   cfg.Recipients,
		materials:    cfg.Materials,
		senderDomain: senderDomain,
		clock:        clock,
		seen:         make(map[string]struct{}),
	}, nil
}

// Publish реализует contracts.Publisher: принимает утверждённое действие и
// ставит собранную команду в очередь. Повтор уже принятого действия — успех без
// второго эффекта (идемпотентность контракта). Доменный отказ и недоступность
// возвращаются как ошибки и не маскируются под успех.
func (a *Adapter) Publish(ctx context.Context, action contracts.ApprovedAction) error {
	_, err := a.Enqueue(ctx, action)
	return err
}

// Enqueue принимает утверждённое действие и возвращает исход приёма.
//
// Порядок шагов: проверка контракта и канала (без внешнего вызова), резервирование
// ключа идемпотентности (тенант, approved_action_id), разрешение получателя и
// материала через порты и постановка команды в очередь. Ключ освобождается при
// любой ошибке, поэтому неуспешный приём не «съедает» действие и повтор вправе
// выполнить его снова.
func (a *Adapter) Enqueue(ctx context.Context, action contracts.ApprovedAction) (Result, error) {
	if a == nil {
		return Result{}, errors.New("approvedaction: адаптер не собран")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := action.Validate(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	if !strings.EqualFold(strings.TrimSpace(action.Channel), contracts.ChannelEmailName) {
		return Result{}, fmt.Errorf("%w: канал %q не поддерживается (только email)", ErrRejected, action.Channel)
	}

	commandID := domain.CommandID(action.ApprovedActionID)
	key := idempotencyKey(action.TenantID, action.ApprovedActionID)
	if !a.reserve(key) {
		return Result{CommandID: commandID, Duplicate: true}, nil
	}

	address, err := a.resolveRecipient(ctx, action)
	if err != nil {
		a.release(key)
		return Result{}, err
	}
	subject, body, err := a.resolveMaterial(ctx, action)
	if err != nil {
		a.release(key)
		return Result{}, err
	}

	command := domain.DeliveryCommand{
		CommandID:        commandID,
		TenantID:         domain.TenantID(action.TenantID),
		ApprovedActionID: domain.ApprovedActionID(action.ApprovedActionID),
		RecipientRef:     domain.RecipientRef(action.RecipientRef),
		Address:          address,
		SenderDomain:     a.senderDomain,
		Subject:          subject,
		Body:             body,
		CorrelationID:    domain.CorrelationID(action.CorrelationID),
		CausationID:      action.DecisionID,
		RequestedAt:      a.clock.Now(),
	}
	if err := a.queue.Enqueue(command); err != nil {
		a.release(key)
		return Result{}, fmt.Errorf("approvedaction: постановка команды в очередь: %w", err)
	}
	return Result{CommandID: commandID}, nil
}

// Next реализует ports.CommandSource: выдаёт следующую собранную команду роли
// sender.
func (a *Adapter) Next(ctx context.Context) (domain.DeliveryCommand, bool, error) {
	if a == nil {
		return domain.DeliveryCommand{}, false, nil
	}
	return a.queue.Next(ctx)
}

// Close завершает выдачу команд. Повторный вызов безопасен.
func (a *Adapter) Close() {
	if a == nil {
		return
	}
	a.queue.Close()
}

// resolveRecipient разрешает ссылку получателя в проверенный адрес. Недоступность
// и неразрешимость возвращаются как явный отказ (ErrUnavailable/ErrRejected),
// адрес не выдумывается.
func (a *Adapter) resolveRecipient(ctx context.Context, action contracts.ApprovedAction) (domain.DeliveryAddress, error) {
	raw, err := a.recipients.ResolveRecipient(ctx, action.TenantID, action.RecipientRef)
	if err != nil {
		return domain.DeliveryAddress{}, classifyResolverError(err, "получатель")
	}
	address, parseErr := domain.ParseDeliveryAddress(raw)
	if parseErr != nil {
		return domain.DeliveryAddress{}, fmt.Errorf("%w: адрес получателя %q: %w", ErrRejected, action.RecipientRef, parseErr)
	}
	return address, nil
}

// resolveMaterial разрешает необязательную ссылку на версию материала в тему и
// тело письма. Пустая ссылка — не ошибка: содержимое остаётся пустым. Заданная
// ссылка без резолвера — честная недоступность.
func (a *Adapter) resolveMaterial(ctx context.Context, action contracts.ApprovedAction) (string, string, error) {
	ref := strings.TrimSpace(action.MaterialVersionRef)
	if ref == "" {
		return "", "", nil
	}
	if a.materials == nil {
		return "", "", fmt.Errorf("%w: резолвер материала не подключён при ссылке %q", ErrUnavailable, ref)
	}
	material, err := a.materials.ResolveMaterial(ctx, action.TenantID, ref)
	if err != nil {
		return "", "", classifyResolverError(err, "материал")
	}
	return material.Subject, material.Body, nil
}

// classifyResolverError переводит ошибку порта-резолвера в исход моста. Отмена и
// дедлайн пробрасываются как есть; явная неразрешимость — доменный отказ; всё
// прочее (в том числе явная недоступность) — честная недоступность. Неизвестная
// ошибка не выдаётся за разрешённое значение.
func classifyResolverError(err error, what string) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ports.ErrReferenceUnresolved):
		return fmt.Errorf("%w: %s: %w", ErrRejected, what, err)
	default:
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, what, err)
	}
}

// idempotencyKey возвращает ключ моста (тенант, approved_action_id). Кодирование
// совпадает с domain.DeliveryCommand.IdempotencyKey, поэтому ключ моста и ключ
// сообщения совпадают.
func idempotencyKey(tenantID, approvedActionID string) string {
	return (domain.DeliveryCommand{
		TenantID:         domain.TenantID(tenantID),
		ApprovedActionID: domain.ApprovedActionID(approvedActionID),
	}).IdempotencyKey()
}

// reserve отмечает ключ как принятый. false означает, что действие уже принято.
func (a *Adapter) reserve(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.seen[key]; ok {
		return false
	}
	a.seen[key] = struct{}{}
	return true
}

// release снимает резервирование ключа при неуспешном приёме.
func (a *Adapter) release(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.seen, key)
}
