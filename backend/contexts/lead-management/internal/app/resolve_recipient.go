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
	// RecipientRef — непрозрачная ссылка на получателя (`RecipientRef`
	// утверждённого действия). Сначала трактуется как идентификатор контакта; если
	// такого контакта нет — как идентификатор компании, у которой берётся контакт
	// с адресом (владелец контракта `campaign-command` разрешает ссылку-компанию).
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
// Адрес берётся только из атрибута email контакта: сначала ссылка трактуется как
// идентификатор контакта, а если такого контакта нет — как идентификатор компании,
// у которой берётся контакт с адресом (так контракт `campaign-command` разрешает
// непрозрачную ссылку-компанию). Отсутствие контакта/компании с адресом, чужой
// тенант — доменный отказ (ErrInvalidDeliveryTarget); отсутствие
// репозитория или сбой чтения — недоступность (ErrDeliveryTargetUnavailable).
// Адрес не подставляется и не маскируется под успех (AGENTS.md §3).
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
	if err != nil && !errors.Is(err, ports.ErrContactNotFound) {
		return ResolvedRecipient{}, fmt.Errorf("%w: чтение контакта %s: %v", ErrDeliveryTargetUnavailable, ref, err)
	}
	if contact == nil {
		// Ссылка не является идентификатором контакта. Контракт `campaign-command`
		// допускает непрозрачную ссылку-компанию (CompanyID) как минимального
		// получателя и требует, чтобы владелец разрешил адрес на своей стороне:
		// пробуем ссылку как компанию и берём её контакт с адресом.
		return u.resolveCompanyContact(ctx, ref)
	}
	email := contact.Email()
	if email == nil || strings.TrimSpace(email.Value) == "" {
		return ResolvedRecipient{}, fmt.Errorf("%w: у контакта %s нет адреса", ErrInvalidDeliveryTarget, ref)
	}
	return ResolvedRecipient{Address: strings.TrimSpace(email.Value)}, nil
}

// resolveCompanyContact разрешает непрозрачную ссылку-компанию в адрес её
// контакта. Берётся первый контакт компании с заполненным адресом; адрес никогда
// не выдумывается — при отсутствии контакта с email это доменный отказ.
func (u *ResolveDeliveryTarget) resolveCompanyContact(ctx context.Context, ref string) (ResolvedRecipient, error) {
	contacts, err := u.Contacts.ListByCompany(ctx, domain.CompanyID(ref))
	if err != nil {
		return ResolvedRecipient{}, fmt.Errorf("%w: чтение контактов компании %s: %v", ErrDeliveryTargetUnavailable, ref, err)
	}
	for _, contact := range contacts {
		if contact == nil {
			continue
		}
		if email := contact.Email(); email != nil && strings.TrimSpace(email.Value) != "" {
			return ResolvedRecipient{Address: strings.TrimSpace(email.Value)}, nil
		}
	}
	return ResolvedRecipient{}, fmt.Errorf("%w: у компании %s нет контакта с адресом", ErrInvalidDeliveryTarget, ref)
}
