package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/mail"
)

// ErrLeadSuppressed — лид подавлен (bounced/unsubscribed/rejected) и исключён из
// рассылки.
var ErrLeadSuppressed = errors.New("app: лид подавлен и исключён из рассылки")

// ErrNoRecipient — у лида нет контакта с адресом для отправки.
var ErrNoRecipient = errors.New("app: у лида нет получателя")

// SendToLead — use case отправки письма лиду из карточки. Письмо собирается из
// шаблона библиотеки и фиксируется в outbound-message ссылкой и снимком тела.
type SendToLead struct {
	Profiles  ports.LeadProfileRepository
	Contacts  ports.ContactRepository
	Messages  ports.OutboundMessageRepository
	Templates ports.EmailTemplateRepository
	Mail      mail.Port
	From      string
	Clock     ports.Clock

	// Events — издатель нормализованных событий дашборда (message-sent). nil —
	// публикация не собрана. TenantID — тенант, в срезе которого учитывается
	// событие.
	Events   ports.AnalyticsPublisher
	TenantID string

	Logger      observ.Logger
	Metrics     observ.Metrics
	ServiceName string
}

// SendResult — исход отправки письма.
type SendResult struct {
	MessageID string
	Status    domain.MessageStatus
	Reason    string
}

// Send отправляет письмо лиду по шаблону библиотеки. templateID — идентификатор
// шаблона; idempotencyKey — ключ идемпотентности формы отправки (идентификатор
// сообщения). tenantID — активный тенант запроса; пусто — берётся тенант
// конфигурации сценария (TenantID). overrides переопределяют значения объявленных
// переменных, если оператор отредактировал их при отправке. Возвращает
// ErrLeadSuppressed, если лид подавлен исходами прошлых сообщений, и
// ErrTemplateNotFound, если шаблон отсутствует.
func (u *SendToLead) Send(ctx context.Context, leadID, templateID string, overrides map[string]string, idempotencyKey, tenantID string) (SendResult, error) {
	company, err := u.Profiles.FindByID(ctx, domain.CompanyID(leadID))
	if errors.Is(err, ports.ErrProfileNotFound) {
		return SendResult{}, ErrCardNotFound
	}
	if err != nil {
		return SendResult{}, err
	}

	contact, err := resolveContact(ctx, u.Contacts, company.ID())
	if err != nil {
		return SendResult{}, err
	}

	suppressed, err := leadSuppressed(ctx, u.Messages, company.ID())
	if err != nil {
		return SendResult{}, err
	}

	template, err := loadEmailTemplate(ctx, u.Templates, templateID)
	if err != nil {
		return SendResult{}, err
	}
	subject, body := ComposeEmailWithValues(template, company, contact, overrides)

	recipient := contact.Email().Value
	message, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:         idempotencyKey,
		LeadID:     company.ID().String(),
		Recipient:  recipient,
		Template:   template.Reference().String(),
		Body:       body,
		Suppressed: suppressed,
		CreatedAt:  u.now(),
	})
	if errors.Is(err, domain.ErrSuppressedRecipient) {
		return SendResult{}, ErrLeadSuppressed
	}
	if err != nil {
		return SendResult{}, err
	}

	if err := u.Mail.Send(ctx, mail.Message{MessageID: message.ID().String(), From: u.From, To: recipient, Subject: subject, Body: body}); err != nil {
		u.Metrics.Incr("downstream_errors_total", map[string]string{"service": u.ServiceName, "engine": "mail"})
		message.MarkFailed(err.Error(), u.now())
		if saveErr := u.Messages.Save(ctx, message); saveErr != nil {
			return SendResult{}, saveErr
		}
		return SendResult{MessageID: message.ID().String(), Status: message.Status(), Reason: message.Reason()}, err
	}

	message.MarkSent(u.now())
	if err := u.Messages.Save(ctx, message); err != nil {
		return SendResult{}, err
	}
	u.publishSent(ctx, company.ID().String(), message, tenantID)
	return SendResult{MessageID: message.ID().String(), Status: message.Status()}, nil
}

// publishSent публикует событие message-sent дашборда в срезе активного тенанта
// запроса. Публикация — вторичный эффект: письмо уже ушло, и её сбой не отменяет
// отправку. Ошибка фиксируется метрикой и логом, а не возвращается вызывающему.
func (u *SendToLead) publishSent(ctx context.Context, companyID string, message *domain.OutboundMessage, tenantID string) {
	if u.Events == nil {
		return
	}
	event := ports.AnalyticsEvent{
		EventID:    message.ID().String(),
		TenantID:   tenantOrDefault(tenantID, u.TenantID),
		Kind:       ports.AnalyticsKindMessageSent,
		CompanyID:  companyID,
		Status:     string(domain.MessageStatusSent),
		Outcome:    string(domain.MessageStatusSent),
		OccurredAt: message.UpdatedAt(),
	}
	if err := u.Events.Publish(ctx, event); err != nil {
		u.Metrics.Incr("downstream_errors_total", map[string]string{"service": u.ServiceName, "engine": "analytics"})
		if u.Logger != nil {
			u.Logger.Warn("send: публикация события дашборда не удалась", observ.Fields{}, "service", u.ServiceName, "event_id", event.EventID, "error", err.Error())
		}
	}
}

// SendToSegment — use case рассылки по сегменту лидов с исключением подавленных
// (bounced/unsubscribed/rejected).
type SendToSegment struct {
	Profiles  ports.LeadProfileRepository
	Contacts  ports.ContactRepository
	Messages  ports.OutboundMessageRepository
	Templates ports.EmailTemplateRepository
	Mail      mail.Port
	From      string
	Clock     ports.Clock

	Metrics     observ.Metrics
	ServiceName string
}

// SegmentResult — исход рассылки по сегменту.
type SegmentResult struct {
	Sent    int
	Skipped int
	Failed  int
}

// Send отправляет письмо каждому лиду сегмента по шаблону библиотеки, исключая
// подавленных и лидов без получателя. Значения переменных берутся из каталога
// для каждого лида отдельно. batchKey — общий ключ идемпотентности; ключ каждого
// сообщения — batchKey + ":" + идентификатор лида.
func (u *SendToSegment) Send(ctx context.Context, leadIDs []string, templateID, batchKey string) (SegmentResult, error) {
	template, err := loadEmailTemplate(ctx, u.Templates, templateID)
	if err != nil {
		return SegmentResult{}, err
	}
	suppressed, err := u.Messages.SuppressedLeads(ctx)
	if err != nil {
		return SegmentResult{}, err
	}
	suppressedSet := make(map[domain.CompanyID]bool, len(suppressed))
	for _, leadID := range suppressed {
		suppressedSet[leadID] = true
	}

	var result SegmentResult
	for _, rawID := range leadIDs {
		leadID := domain.CompanyID(strings.TrimSpace(rawID))
		if suppressedSet[leadID] {
			result.Skipped++
			continue
		}

		company, err := u.Profiles.FindByID(ctx, leadID)
		if errors.Is(err, ports.ErrProfileNotFound) {
			result.Skipped++
			continue
		}
		if err != nil {
			return result, err
		}

		contact, err := resolveContact(ctx, u.Contacts, leadID)
		if errors.Is(err, ErrNoRecipient) {
			result.Skipped++
			continue
		}
		if err != nil {
			return result, err
		}

		recipient := contact.Email().Value
		subject, body := ComposeEmail(template, company, contact)

		message, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
			ID:        batchKey + ":" + leadID.String(),
			LeadID:    leadID.String(),
			Recipient: recipient,
			Template:  template.Reference().String(),
			Body:      body,
			CreatedAt: u.now(),
		})
		if err != nil {
			return result, err
		}

		if err := u.Mail.Send(ctx, mail.Message{MessageID: message.ID().String(), From: u.From, To: recipient, Subject: subject, Body: body}); err != nil {
			u.Metrics.Incr("downstream_errors_total", map[string]string{"service": u.ServiceName, "engine": "mail"})
			message.MarkFailed(err.Error(), u.now())
			_ = u.Messages.Save(ctx, message)
			result.Failed++
			continue
		}

		message.MarkSent(u.now())
		if err := u.Messages.Save(ctx, message); err != nil {
			return result, err
		}
		result.Sent++
	}
	return result, nil
}

// resolveContact возвращает первый контакт лида с заполненным email, либо
// ErrNoRecipient.
func resolveContact(ctx context.Context, contacts ports.ContactRepository, leadID domain.CompanyID) (*domain.Contact, error) {
	list, err := contacts.ListByCompany(ctx, leadID)
	if err != nil {
		return nil, err
	}
	for _, contact := range list {
		if email := contact.Email(); email != nil && strings.TrimSpace(email.Value) != "" {
			return contact, nil
		}
	}
	return nil, ErrNoRecipient
}

// leadSuppressed сообщает, подавлен ли лид исходом хотя бы одного прошлого
// сообщения.
func leadSuppressed(ctx context.Context, messages ports.OutboundMessageRepository, leadID domain.CompanyID) (bool, error) {
	list, err := messages.ListByLead(ctx, leadID)
	if err != nil {
		return false, err
	}
	for _, message := range list {
		if message.Status().Suppresses() {
			return true, nil
		}
	}
	return false, nil
}

// loadEmailTemplate загружает шаблон библиотеки, отображая отсутствие (и
// незаданный репозиторий) в ErrTemplateNotFound.
func loadEmailTemplate(ctx context.Context, repository ports.EmailTemplateRepository, id string) (*domain.EmailTemplate, error) {
	if repository == nil {
		return nil, ErrTemplateNotFound
	}
	template, err := repository.FindByID(ctx, domain.TemplateID(strings.TrimSpace(id)))
	if errors.Is(err, ports.ErrTemplateNotFound) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	return template, nil
}

// now возвращает текущий момент часов либо системное время.
func (u *SendToLead) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// now возвращает текущий момент часов либо системное время.
func (u *SendToSegment) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}
