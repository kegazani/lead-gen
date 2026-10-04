package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// VerificationStatus — классификация адреса по доставляемости и риску
// (capability email-verification). Статус — производная оценка по протоколу, а не
// утверждение о существовании ящика: формулировка «адрес существует» запрещена.
type VerificationStatus string

// Согласованный набор из семи статусов. Значение указывает на риск, но не
// подтверждает наличие ящика.
const (
	// StatusDeliverable — синтаксис, домен и MX в порядке; сервер принял получателя.
	StatusDeliverable VerificationStatus = "deliverable"
	// StatusRisky — принимающий домен (catch-all) или иной слабый сигнал: статус не
	// выше risky присваивается, даже если сервер принял адрес.
	StatusRisky VerificationStatus = "risky"
	// StatusUnknown — сервер не раскрывает наличие ящика (Gmail, Outlook).
	StatusUnknown VerificationStatus = "unknown"
	// StatusInvalid — недействительный адрес: синтаксис, домен, MX или отказ получателя.
	StatusInvalid VerificationStatus = "invalid"
	// StatusDisposable — одноразовый или временный домен.
	StatusDisposable VerificationStatus = "disposable"
	// StatusRole — ролевой адрес (info@, sales@, support@ и подобные).
	StatusRole VerificationStatus = "role"
	// StatusAbuse — известный плохой адрес или ранее полученная жалоба.
	StatusAbuse VerificationStatus = "abuse"
)

// Known сообщает, входит ли статус в согласованный набор.
func (s VerificationStatus) Known() bool {
	switch s {
	case StatusDeliverable, StatusRisky, StatusUnknown, StatusInvalid, StatusDisposable, StatusRole, StatusAbuse:
		return true
	default:
		return false
	}
}

// BlocksSend сообщает, исключает ли статус адрес из отправки независимо от
// политики: недействительный, одноразовый и известный плохой адрес.
func (s VerificationStatus) BlocksSend() bool {
	switch s {
	case StatusInvalid, StatusDisposable, StatusAbuse:
		return true
	default:
		return false
	}
}

// CapForCatchAll понижает статус адреса catch-all домена до не выше risky.
// Catch-all домен принимает любой адрес, поэтому ответ сервера о принятии не
// несёт информации о ящике, и статус deliverable присвоить нельзя.
func CapForCatchAll(s VerificationStatus) VerificationStatus {
	if s == StatusDeliverable {
		return StatusRisky
	}
	return s
}

// ContactAddress — value object contact-address: разобранный адрес по RFC 5322.
// Local сохраняется как есть (регистр значим для локальной части), Domain
// нормализуется в нижний регистр.
type ContactAddress struct {
	local  string
	domain string
}

// ParseContactAddress разбирает адрес по RFC 5322. Неверный синтаксис даёт
// ErrInvalidAddress без обращения к внешнему сервису.
func ParseContactAddress(raw string) (ContactAddress, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ContactAddress{}, fmt.Errorf("%w: пустой адрес", ErrInvalidAddress)
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil {
		return ContactAddress{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	at := strings.LastIndex(parsed.Address, "@")
	if at <= 0 || at == len(parsed.Address)-1 {
		return ContactAddress{}, fmt.Errorf("%w: %q", ErrInvalidAddress, trimmed)
	}
	local := parsed.Address[:at]
	domain := strings.ToLower(parsed.Address[at+1:])
	if strings.ContainsAny(local, " \t") || strings.ContainsAny(domain, " \t") {
		return ContactAddress{}, fmt.Errorf("%w: %q", ErrInvalidAddress, trimmed)
	}
	return ContactAddress{local: local, domain: domain}, nil
}

// Local возвращает локальную часть адреса.
func (a ContactAddress) Local() string { return a.local }

// Domain возвращает нормализованный (нижний регистр) домен адреса.
func (a ContactAddress) Domain() string { return a.domain }

// String возвращает адрес в каноническом виде local@domain.
func (a ContactAddress) String() string {
	if a.local == "" || a.domain == "" {
		return ""
	}
	return a.local + "@" + a.domain
}

// roleLocals — локальные части, за которыми стоит функция, а не человек
// (capability email-verification). Набор закрыт для этого среза и расширяется
// настройкой.
var roleLocals = map[string]struct{}{
	"info": {}, "sales": {}, "support": {}, "admin": {}, "contact": {},
	"hello": {}, "office": {}, "billing": {}, "hr": {}, "marketing": {},
	"team": {}, "help": {}, "noreply": {}, "no-reply": {}, "postmaster": {},
	"webmaster": {}, "jobs": {}, "careers": {}, "press": {}, "feedback": {},
}

// IsRoleAddress сообщает, является ли локальная часть ролевой. Регистр не значим.
func IsRoleAddress(local string) bool {
	_, ok := roleLocals[strings.ToLower(strings.TrimSpace(local))]
	return ok
}

// disposableDomains — поддерживаемый список одноразовых и временных доменов
// (capability email-verification). Состав списка — открытый вопрос BRD §8 (вопрос
// 4): набор расширяется настройкой без изменения классификатора.
var disposableDomains = map[string]struct{}{
	"mailinator.com": {}, "tempmail.com": {}, "10minutemail.com": {},
	"guerrillamail.com": {}, "sharklasers.com": {}, "yopmail.com": {},
	"trashmail.com": {}, "maildrop.cc": {}, "dispostable.com": {},
	"getnada.com": {}, "throwaway.email": {}, "temp-mail.org": {},
}

// IsDisposableDomain сообщает, входит ли домен в список одноразовых и временных.
// Регистр не значим.
func IsDisposableDomain(domain string) bool {
	_, ok := disposableDomains[strings.ToLower(strings.TrimSpace(domain))]
	return ok
}

// CheckedEmail — результат проверки адреса: исходный адрес, разбор, статус,
// причина, дата и источник проверки (requirement «Классификация адреса по
// статусам»). Пустые причина или источник недопустимы: результат без основания не
// является проверкой. Для неверного синтаксиса разбор пуст, а исходный адрес
// сохранён — статус invalid остаётся наблюдаемым.
type CheckedEmail struct {
	Raw       string
	Address   ContactAddress
	Status    VerificationStatus
	Reason    string
	Source    string
	CheckedAt time.Time
	CatchAll  bool
}

// NewCheckedEmail собирает результат проверки, проверяя согласованность: статус
// известен, причина и источник непусты, дата задана, исходный адрес непустой.
func NewCheckedEmail(raw string, address ContactAddress, status VerificationStatus, reason, source string, checkedAt time.Time, catchAll bool) (CheckedEmail, error) {
	if !status.Known() {
		return CheckedEmail{}, fmt.Errorf("%w: %q", ErrInvalidVerificationStatus, status)
	}
	if strings.TrimSpace(reason) == "" {
		return CheckedEmail{}, fmt.Errorf("%w: причина не указана", ErrInvalidVerificationResult)
	}
	if strings.TrimSpace(source) == "" {
		return CheckedEmail{}, fmt.Errorf("%w: источник не указан", ErrInvalidVerificationResult)
	}
	if checkedAt.IsZero() {
		return CheckedEmail{}, fmt.Errorf("%w: дата проверки не указана", ErrInvalidVerificationResult)
	}
	if strings.TrimSpace(raw) == "" {
		return CheckedEmail{}, fmt.Errorf("%w: исходный адрес не указан", ErrInvalidVerificationResult)
	}
	return CheckedEmail{
		Raw:       strings.TrimSpace(raw),
		Address:   address,
		Status:    status,
		Reason:    strings.TrimSpace(reason),
		Source:    strings.TrimSpace(source),
		CheckedAt: checkedAt.UTC(),
		CatchAll:  catchAll,
	}, nil
}

// SendDecision — решение политики по статусу: отправлять, отправлять с
// ограничением доли или не отправлять.
type SendDecision string

// Согласованные решения политики.
const (
	// DecisionAllow — адрес допущен к отправке без ограничения доли.
	DecisionAllow SendDecision = "allow"
	// DecisionLimit — адрес допущен с ограничением доли в кампании.
	DecisionLimit SendDecision = "limit"
	// DecisionBlock — адрес исключён из отправки (при BlocksSend — независимо от политики).
	DecisionBlock SendDecision = "block"
)

// VerificationPolicy — настраиваемая политика отправки по статусам. Значения по
// умолчанию — предложение design.md (Решения 4), подлежащее калибровке: числа не
// выдуманы как факт и помечены непроверенными.
type VerificationPolicy struct {
	decisions map[VerificationStatus]SendDecision
	maxShare  map[VerificationStatus]float64
}

// DefaultVerificationPolicy возвращает политику по умолчанию: deliverable —
// отправлять; role/unknown — с ограничением доли; risky — с меньшей долей; invalid/
// disposable/abuse — не отправлять.
func DefaultVerificationPolicy() VerificationPolicy {
	return VerificationPolicy{
		decisions: map[VerificationStatus]SendDecision{
			StatusDeliverable: DecisionAllow,
			StatusRole:        DecisionLimit,
			StatusUnknown:     DecisionLimit,
			StatusRisky:       DecisionLimit,
			StatusInvalid:     DecisionBlock,
			StatusDisposable:  DecisionBlock,
			StatusAbuse:       DecisionBlock,
		},
		// Доли — предлагаемые значения, требующие калибровки (BRD §8, вопрос 9b).
		maxShare: map[VerificationStatus]float64{
			StatusRole:    0.20,
			StatusUnknown: 0.50,
			StatusRisky:   0.15,
		},
	}
}

// Decision возвращает решение политики по статусу и предельную долю для
// ограниченных статусов (0 — без ограничения). Блокирующие статусы исключаются
// независимо от политики.
func (p VerificationPolicy) Decision(status VerificationStatus) (SendDecision, float64) {
	if status.BlocksSend() {
		return DecisionBlock, 0
	}
	if decision, ok := p.decisions[status]; ok {
		return decision, p.maxShare[status]
	}
	// Неизвестный политике статус к отправке не допускается: политика по умолчанию
	// консервативна.
	return DecisionBlock, 0
}
