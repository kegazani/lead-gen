package approvedaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ErrUnavailable     = errors.New("approvedaction: резолвер ссылок недоступен")
	ErrPayloadConflict = errors.New("approvedaction: конфликт payload для ключа идемпотентности")
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

	// DurableQueue заменяет локальную очередь/seen при подключении PostgreSQL.
	// nil разрешён только для тестов и dev.
	DurableQueue ports.DurableCommandQueue
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
	durableQueue ports.DurableCommandQueue
	recipients   ports.RecipientResolver
	materials    ports.MaterialResolver
	senderDomain domain.SenderDomain
	clock        ports.Clock

	mu        sync.Mutex
	seen      map[string]string
	cancelled map[string]struct{}
}

// Адаптер реализует источник команд роли sender и потребительский интерфейс
// контракта approved-action.
var (
	_ ports.CommandSource        = (*Adapter)(nil)
	_ ports.ClaimedCommandSource = (*Adapter)(nil)
	_ contracts.Publisher        = (*Adapter)(nil)
	_ contracts.Canceller        = (*Adapter)(nil)
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
		durableQueue: cfg.DurableQueue,
		recipients:   cfg.Recipients,
		materials:    cfg.Materials,
		senderDomain: senderDomain,
		clock:        clock,
		seen:         make(map[string]string),
		cancelled:    make(map[string]struct{}),
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
// материала через порты и постановка команды в очередь. Идентификатор
// утверждённого действия уникален в рамках кампании-владельца, поэтому действие
// другой кампании к тому же получателю даёт другой ключ и не считается повтором;
// повтор одного действия ключ не меняет. Ключ освобождается при любой ошибке,
// поэтому неуспешный приём не «съедает» действие и повтор вправе выполнить его
// снова.
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
	if a.durableQueue == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, cancelled := a.cancelled[campaignKey(action.TenantID, action.CampaignID)]; cancelled {
			return Result{}, ports.ErrCampaignCancelled
		}
	} else {
		reader, ok := a.durableQueue.(ports.CampaignFenceReader)
		if !ok {
			return Result{}, errors.New("approvedaction: durable queue не поддерживает cancellation fence")
		}
		cancelled, err := reader.CampaignCancelled(ctx, action.TenantID, action.CampaignID)
		if err != nil {
			return Result{}, err
		}
		if cancelled {
			return Result{}, ports.ErrCampaignCancelled
		}
	}

	commandID := domain.CommandID(action.ApprovedActionID)
	key := idempotencyKey(action.TenantID, action.ApprovedActionID)
	var memoryPayloadHash string
	if a.durableQueue == nil {
		canonical, err := json.Marshal(struct {
			TenantID, ApprovedActionID, CampaignID, DraftID, DecisionID           string
			DraftVersion                                                          int64
			Channel, RecipientRef, MaterialVersionRef, CorrelationID, CausationID string
			DecidedAt                                                             time.Time
		}{action.TenantID, action.ApprovedActionID, action.CampaignID, action.DraftID, action.DecisionID,
			action.DraftVersion, action.Channel, action.RecipientRef, action.MaterialVersionRef,
			action.CorrelationID, action.CausationID, action.DecidedAt.UTC()})
		if err != nil {
			return Result{}, err
		}
		sum := sha256.Sum256(canonical)
		memoryPayloadHash = hex.EncodeToString(sum[:])
		if prior, exists := a.seen[key]; exists {
			if prior != memoryPayloadHash {
				return Result{}, fmt.Errorf("%w: %s", ErrPayloadConflict, key)
			}
			return Result{CommandID: commandID, Duplicate: true}, nil
		}
	}
	address, err := a.resolveRecipient(ctx, action)
	if err != nil {
		return Result{}, err
	}
	subject, body, err := a.resolveMaterial(ctx, action)
	if err != nil {
		return Result{}, err
	}

	command := domain.DeliveryCommand{
		CommandID:        commandID,
		TenantID:         domain.TenantID(action.TenantID),
		ApprovedActionID: domain.ApprovedActionID(action.ApprovedActionID),
		CampaignID:       action.CampaignID,
		RecipientRef:     domain.RecipientRef(action.RecipientRef),
		Address:          address,
		SenderDomain:     a.senderDomain,
		Subject:          subject,
		Body:             body,
		CorrelationID:    domain.CorrelationID(action.CorrelationID),
		CausationID:      action.DecisionID,
		RequestedAt:      a.clock.Now(),
	}
	if a.durableQueue != nil {
		duplicate, err := a.durableQueue.Enqueue(ctx, command)
		if err != nil {
			return Result{}, fmt.Errorf("approvedaction: durable постановка команды: %w", err)
		}
		return Result{CommandID: commandID, Duplicate: duplicate}, nil
	}
	if err := a.queue.Enqueue(command); err != nil {
		return Result{}, fmt.Errorf("approvedaction: постановка команды в очередь: %w", err)
	}
	a.seen[key] = memoryPayloadHash
	return Result{CommandID: commandID}, nil
}

// CancelCampaign records a memory tombstone or delegates to the durable inbox.
func (a *Adapter) CancelCampaign(ctx context.Context, cancellation contracts.CampaignCancellation) error {
	if a == nil {
		return errors.New("approvedaction: адаптер не собран")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cancellation.Validate(); err != nil {
		return err
	}
	if a.durableQueue != nil {
		canceller, ok := a.durableQueue.(ports.CampaignCancellationQueue)
		if !ok {
			return errors.New("approvedaction: durable queue не поддерживает отмену кампании")
		}
		return canceller.CancelCampaign(ctx, cancellation.TenantID, cancellation.CampaignID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled[campaignKey(cancellation.TenantID, cancellation.CampaignID)] = struct{}{}
	return nil
}

func (a *Adapter) CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error) {
	if a == nil {
		return false, errors.New("approvedaction: адаптер не собран")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if a.durableQueue != nil {
		reader, ok := a.durableQueue.(ports.CampaignFenceReader)
		if !ok {
			return false, errors.New("approvedaction: durable queue не поддерживает cancellation fence")
		}
		return reader.CampaignCancelled(ctx, tenantID, campaignID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, cancelled := a.cancelled[campaignKey(tenantID, campaignID)]
	return cancelled, nil
}

func campaignKey(tenantID, campaignID string) string { return tenantID + "\x00" + campaignID }

// Next реализует ports.CommandSource: выдаёт следующую собранную команду роли
// sender.
func (a *Adapter) Next(ctx context.Context) (domain.DeliveryCommand, bool, error) {
	if a == nil {
		return domain.DeliveryCommand{}, false, nil
	}
	if a.durableQueue != nil {
		cmd, _, ok, err := a.durableQueue.Claim(ctx)
		return cmd, ok, err
	}
	for {
		cmd, ok, err := a.queue.Next(ctx)
		if err != nil || !ok {
			return cmd, ok, err
		}
		a.mu.Lock()
		_, cancelled := a.cancelled[campaignKey(cmd.TenantID.String(), cmd.CampaignID)]
		a.mu.Unlock()
		if !cancelled {
			return cmd, true, nil
		}
	}
}

// Claim/Ack/Release реализуют durable lease API. При memory-режиме Claim
// использует Next, а ACK не нужен — этот вариант не предназначен production.
func (a *Adapter) Claim(ctx context.Context) (domain.DeliveryCommand, string, bool, error) {
	if a.durableQueue == nil {
		cmd, ok, err := a.Next(ctx)
		return cmd, "", ok, err
	}
	return a.durableQueue.Claim(ctx)
}

func (a *Adapter) Ack(ctx context.Context, token string) error {
	if a.durableQueue == nil {
		return nil
	}
	return a.durableQueue.Ack(ctx, token)
}

func (a *Adapter) Release(ctx context.Context, token string) error {
	if a.durableQueue == nil {
		return nil
	}
	return a.durableQueue.Release(ctx, token)
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
// сообщения совпадают. Идентификатор утверждённого действия уникален в рамках
// кампании (в него входит кампания-владелец), поэтому действия разных кампаний к
// одному получателю дают разные ключи и доставляются каждое.
func idempotencyKey(tenantID, approvedActionID string) string {
	return (domain.DeliveryCommand{
		TenantID:         domain.TenantID(tenantID),
		ApprovedActionID: domain.ApprovedActionID(approvedActionID),
	}).IdempotencyKey()
}

// reserve отмечает ключ как принятый. false означает, что действие уже принято.
