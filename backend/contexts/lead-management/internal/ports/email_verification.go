package ports

import (
	"context"
	"errors"
)

// Ошибки внешней проверки адресов (capability email-verification). Различают
// доменный исход и сбой: недоступность не выдаётся за статус.
var (
	// ErrVerifierUnavailable — сервис верификации адресов недоступен: провайдер не
	// сконфигурирован, сбой транспорта или нераспознанный ответ. Адрес при этом не
	// получает выдуманного статуса.
	ErrVerifierUnavailable = errors.New("lead-management: сервис верификации адресов недоступен")
	// ErrDomainNotFound — домен адреса не существует (NXDOMAIN).
	ErrDomainNotFound = errors.New("lead-management: домен адреса не существует")
	// ErrResolverUnavailable — DNS-резолвер недоступен: запрос не выполнен.
	ErrResolverUnavailable = errors.New("lead-management: DNS-резолвер недоступен")
)

// MailboxOutcome — нормализованный ответ сервиса верификации о получателе.
// «Принято» не означает, что ящик существует: сервер может вернуть отказ позже.
type MailboxOutcome string

// Согласованные исходы ответа сервиса.
const (
	// MailboxAccepted — сервер принял получателя (ящик не подтверждён).
	MailboxAccepted MailboxOutcome = "accepted"
	// MailboxRejected — получатель отклонён.
	MailboxRejected MailboxOutcome = "rejected"
	// MailboxUnknown — сервер не раскрывает наличие ящика.
	MailboxUnknown MailboxOutcome = "unknown"
)

// MailboxVerdict — ответ сервиса проверки по адресу: исход, признак catch-all
// домена (принимает любой адрес) и основания. CatchAll — свойство домена, а не
// ящика; при нём статус адреса понижается до не выше risky.
type MailboxVerdict struct {
	Outcome  MailboxOutcome
	CatchAll bool
	Reason   string
	Source   string
}

// AddressVerifier — порт внешнего сервиса верификации адресов. Собственный
// SMTP-пробинг не реализуется (Решение 1): проверка выполняется внешним сервисом,
// чтобы массовые пробы не шли с IP отправки. Реализация обязана возвращать
// ErrVerifierUnavailable при недоступности, а не выдуманный исход.
type AddressVerifier interface {
	VerifyMailbox(ctx context.Context, address string) (MailboxVerdict, error)
}

// DNSResolver — порт DNS-проверки домена адреса: записи MX с откатом на запись A
// по RFC 5321. Отсутствие домена — ErrDomainNotFound; сбой резолвера —
// ErrResolverUnavailable.
type DNSResolver interface {
	LookupMX(ctx context.Context, domain string) ([]string, error)
	LookupHost(ctx context.Context, domain string) ([]string, error)
}
