package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ErrInvalidDeliveryTarget — ссылка на получателя не разрешается: пустой тенант
// или ссылка, чужая (принадлежащая другому тенанту) или неизвестная ссылка, либо
// контакт без пригодного адреса. Владелец не выдумывает адрес назначения.
var ErrInvalidDeliveryTarget = errors.New("app: ссылка на получателя не разрешается")

// ErrDeliveryTargetUnavailable — резолвер получателя недоступен: нет репозитория
// контактов, резолвер не привязан к тенанту или сбой чтения. Адрес не выдумывается.
var ErrDeliveryTargetUnavailable = errors.New("app: разрешение получателя недоступно")

// ResolveRecipientInput — вход сценария разрешения получателя: тенант и
// непрозрачная ссылка на контакт.
type ResolveRecipientInput struct {
	// TenantID — тенант-владелец ссылки. Сверяется с тенантом резолвера: чужая
	// ссылка не разрешается.
	TenantID string
	// RecipientRef — непрозрачная ссылка на контакт (`RecipientRef` утверждённого
	// действия). Трактуется как идентификатор контакта.
	RecipientRef string
}

// ResolvedRecipient — разрешённый получатель: проверенный адрес назначения.
type ResolvedRecipient struct {
	// Address — адрес из атрибута контакта (никогда не выдуманный).
	Address string
}

// ResolveDeliveryTarget — use case разрешения `RecipientRef` в адрес назначения по
// контактам (агрегат contact). Резолвер привязан к одному тенанту: красная зона —
// контур клиента, поэтому контакты чужого тенанта через него не разрешаются.
//
// Адрес берётся только из атрибута email контакта. Отсутствие контакта, чужой
// тенант и контакт без адреса — доменный отказ (ErrInvalidDeliveryTarget);
// отсутствие репозитория или сбой чтения — недоступность
// (ErrDeliveryTargetUnavailable). Адрес не подставляется и не маскируется под
// успех (AGENTS.md §3).
type ResolveDeliveryTarget struct {
	// Contacts — репозиторий контактов (красная зона). Обязателен.
	Contacts ports.ContactRepository
	// TenantID — тенант-владелец хранилища контактов этого резолвера. Пусто —
	// резолвер не привязан к тенанту, изоляция не гарантируется, разрешение честно
	// недоступно.
	TenantID string
}

// Run разрешает ссылку в адрес назначения. Пустая или чужая ссылка, неизвестный
// контакт и контакт без адреса дают ErrInvalidDeliveryTarget; неподключённый
// репозиторий, непривязанный тенант и сбой чтения — ErrDeliveryTargetUnavailable.
func (u *ResolveDeliveryTarget) Run(ctx context.Context, in ResolveRecipientInput) (ResolvedRecipient, error) {
	if u == nil || u.Contacts == nil {
		return ResolvedRecipient{}, fmt.Errorf("%w: репозиторий контактов не подключён", ErrDeliveryTargetUnavailable)
	}
	tenant := strings.TrimSpace(in.TenantID)
	ref := strings.TrimSpace(in.RecipientRef)
	if tenant == "" || ref == "" {
		return ResolvedRecipient{}, fmt.Errorf("%w: тенант и ссылка на получателя обязательны", ErrInvalidDeliveryTarget)
	}
	if u.TenantID == "" {
		return ResolvedRecipient{}, fmt.Errorf("%w: резолвер не привязан к тенанту", ErrDeliveryTargetUnavailable)
	}
	// Изоляция тенанта: резолвер обслуживает контур своего тенанта, чужая ссылка
	// не разрешается даже при совпадении идентификатора контакта.
	if tenant != strings.TrimSpace(u.TenantID) {
		return ResolvedRecipient{}, fmt.Errorf("%w: ссылка принадлежит другому тенанту", ErrInvalidDeliveryTarget)
	}

	contact, err := u.Contacts.FindByID(ctx, domain.ContactID(ref))
	if errors.Is(err, ports.ErrContactNotFound) {
		return ResolvedRecipient{}, fmt.Errorf("%w: контакт %s не найден", ErrInvalidDeliveryTarget, ref)
	}
	if err != nil {
		return ResolvedRecipient{}, fmt.Errorf("%w: чтение контакта %s: %v", ErrDeliveryTargetUnavailable, ref, err)
	}
	if contact == nil {
		return ResolvedRecipient{}, fmt.Errorf("%w: контакт %s не найден", ErrInvalidDeliveryTarget, ref)
	}
	email := contact.Email()
	if email == nil || strings.TrimSpace(email.Value) == "" {
		return ResolvedRecipient{}, fmt.Errorf("%w: у контакта %s нет адреса", ErrInvalidDeliveryTarget, ref)
	}
	return ResolvedRecipient{Address: strings.TrimSpace(email.Value)}, nil
}
