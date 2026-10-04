package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ErrVerificationUnavailable — проверка адреса не выполнена: внешний сервис или
// DNS-резолвер не сконфигурирован или недоступен. Адрес не получает выдуманного
// статуса — он остаётся непроверенным.
var ErrVerificationUnavailable = errors.New("app: проверка адреса недоступна")

// VerifyAddress — use case классификации адреса (capability email-verification):
// разбор синтаксиса, DNS-проверка домена (MX с откатом на A), список одноразовых
// доменов, ролевые адреса и проверка получателя внешним сервисом.
//
// Синтаксис проверяется без внешнего вызова; домен — резолвером; получатель —
// внешним сервисом (собственный SMTP-пробинг не выполняется). Слабый сигнал не
// повышает статус: адрес catch-all домена не получает статус выше risky.
type VerifyAddress struct {
	// DNS — резолвер домена. Обязателен: без него домен не проверяется.
	DNS ports.DNSResolver
	// Verifier — внешний сервис проверки получателя. Обязателен: без него проверка
	// недоступна, а не подменяется статусом.
	Verifier ports.AddressVerifier
	// Clock — часы; nil — системное время.
	Clock ports.Clock
}

// VerifyAddressInput — вход проверки: один адрес.
type VerifyAddressInput struct {
	// Address — адрес для проверки.
	Address string
}

// Run проверяет адрес и возвращает результат со статусом, причиной, датой и
// источником. Неверный синтаксис даёт статус `invalid` без обращения к сети;
// недоступность резолвера или сервиса даёт ErrVerificationUnavailable.
func (u *VerifyAddress) Run(ctx context.Context, in VerifyAddressInput) (domain.CheckedEmail, error) {
	now := u.now()
	raw := strings.TrimSpace(in.Address)
	if raw == "" {
		return domain.CheckedEmail{}, fmt.Errorf("%w: пустой адрес", ErrVerificationUnavailable)
	}

	address, parseErr := domain.ParseContactAddress(raw)
	if parseErr != nil {
		// Синтаксис — до внешнего вызова: негодный адрес не порождает запроса.
		return domain.NewCheckedEmail(raw, domain.ContactAddress{}, domain.StatusInvalid,
			"синтаксис адреса не соответствует RFC 5322", "syntax", now, false)
	}
	if u.DNS == nil || u.Verifier == nil {
		return domain.CheckedEmail{}, fmt.Errorf("%w: резолвер или сервис проверки не подключён", ErrVerificationUnavailable)
	}

	if invalid, err := u.checkDomain(ctx, address, raw, now); err != nil || invalid != nil {
		if err != nil {
			return domain.CheckedEmail{}, err
		}
		return *invalid, nil
	}

	if domain.IsDisposableDomain(address.Domain()) {
		return domain.NewCheckedEmail(raw, address, domain.StatusDisposable,
			"домен входит в список одноразовых и временных", "disposable-list", now, false)
	}

	verdict, err := u.Verifier.VerifyMailbox(ctx, address.String())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return domain.CheckedEmail{}, err
		}
		return domain.CheckedEmail{}, fmt.Errorf("%w: %v", ErrVerificationUnavailable, err)
	}

	return u.classify(raw, address, verdict, now)
}

// checkDomain проверяет существование домена и записи MX с откатом на A. Домен
// без записей MX и A даёт результат invalid; отсутствие домена — тоже invalid.
// Недоступность резолвера возвращается ошибкой. Возвращённый *CheckResult — уже
// готовый доменный отказ (invalid).
func (u *VerifyAddress) checkDomain(ctx context.Context, address domain.ContactAddress, raw string, now time.Time) (*domain.CheckedEmail, error) {
	mx, err := u.DNS.LookupMX(ctx, address.Domain())
	if err != nil {
		return u.domainError(address, raw, now, err)
	}
	if len(mx) > 0 {
		return nil, nil
	}
	hosts, err := u.DNS.LookupHost(ctx, address.Domain())
	if err != nil {
		return u.domainError(address, raw, now, err)
	}
	if len(hosts) == 0 {
		result, buildErr := domain.NewCheckedEmail(raw, address, domain.StatusInvalid,
			"у домена нет записей MX и A", "dns", now, false)
		return &result, buildErr
	}
	return nil, nil
}

// domainError переводит ошибку резолвера: отсутствие домена — invalid, прочее —
// недоступность.
func (u *VerifyAddress) domainError(address domain.ContactAddress, raw string, now time.Time, err error) (*domain.CheckedEmail, error) {
	if errors.Is(err, ports.ErrDomainNotFound) {
		result, buildErr := domain.NewCheckedEmail(raw, address, domain.StatusInvalid,
			"домен не существует", "dns", now, false)
		return &result, buildErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return nil, fmt.Errorf("%w: DNS-проверка: %v", ErrVerificationUnavailable, err)
}

// classify сводит ответ сервиса, catch-all и ролевую метку в один статус. Слабый
// сигнал не повышает статус: catch-all понижает до не выше risky, а ролевая
// локальная часть переводит положительный статус в role.
func (u *VerifyAddress) classify(raw string, address domain.ContactAddress, verdict ports.MailboxVerdict, now time.Time) (domain.CheckedEmail, error) {
	var status domain.VerificationStatus
	var reason string
	switch verdict.Outcome {
	case ports.MailboxAccepted:
		status = domain.StatusDeliverable
		reason = "сервер принял получателя; наличие ящика не подтверждено"
	case ports.MailboxRejected:
		status = domain.StatusInvalid
		reason = "сервер отклонил получателя"
	case ports.MailboxUnknown:
		status = domain.StatusUnknown
		reason = "сервер не раскрывает наличие ящика"
	default:
		return domain.CheckedEmail{}, fmt.Errorf("%w: неизвестный исход сервиса %q", ErrVerificationUnavailable, verdict.Outcome)
	}
	if verdict.Reason != "" {
		reason = reason + "; " + verdict.Reason
	}
	if verdict.CatchAll {
		status = domain.CapForCatchAll(status)
		reason = reason + "; домен принимает любой адрес (catch-all)"
	}
	if domain.IsRoleAddress(address.Local()) && status == domain.StatusDeliverable {
		status = domain.StatusRole
		reason = reason + "; ролевой адрес"
	}
	return domain.NewCheckedEmail(raw, address, status, reason, verdict.Source, now, verdict.CatchAll)
}

// now возвращает текущий момент.
func (u *VerifyAddress) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
