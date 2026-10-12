package components

import (
	"io"
	"time"
)

// Словарь верификации адреса (BRD §3.18): «доставляемый», «рискованный»,
// «неизвестно», «недействительный». Формулировка «адрес существует» запрещена:
// верификация адреса — оценка доставляемости, а не факт наличия, и положительное
// «существует» вводит в заблуждение при рискованном и неизвестном исходе
// (web-frontend-architecture, срез 7, 7.2; design.md, Решение 12).

// AddressStatus — статус верификации адреса. Статусы различимы и не смешиваются
// между собой.
type AddressStatus string

// Статусы верификации адреса.
const (
	// AddressDeliverable — адрес принимает почту по данным провайдера.
	AddressDeliverable AddressStatus = "deliverable"
	// AddressRisky — адрес принимает почту, но с признаками риска (роль,
	// одноразовый домен, ловушка).
	AddressRisky AddressStatus = "risky"
	// AddressUnknown — достоверной оценки нет; неизвестность не выдаётся за
	// успех и не срывается к «недействительный».
	AddressUnknown AddressStatus = "unknown"
	// AddressInvalid — адрес не принимает почту (жёсткий отказ, отписка).
	AddressInvalid AddressStatus = "invalid"
)

// Valid сообщает, входит ли статус в словарь.
func (s AddressStatus) Valid() bool {
	_, ok := s.messageKey()
	return ok
}

// Label возвращает подпись статуса из каталога сообщений. Неизвестный статус
// показывается честно как «неизвестно», а не выдумывается.
func (s AddressStatus) Label() string {
	key, ok := s.messageKey()
	if !ok {
		return DefaultMessages().Text(msgVerifyUnknown)
	}
	return DefaultMessages().Text(key)
}

// Icon возвращает значок статуса из каталога сообщений. Значок — не цвет:
// смысл статуса несут подпись и знак, цвет только дополняет.
func (s AddressStatus) Icon() string {
	iconKey, ok := s.iconKey()
	if !ok {
		return DefaultMessages().Text(msgVerifyIconUnknown)
	}
	return DefaultMessages().Text(iconKey)
}

// messageKey сопоставляет статус подписи каталога.
func (s AddressStatus) messageKey() (MessageKey, bool) {
	switch s {
	case AddressDeliverable:
		return msgVerifyDeliverable, true
	case AddressRisky:
		return msgVerifyRisky, true
	case AddressUnknown:
		return msgVerifyUnknown, true
	case AddressInvalid:
		return msgVerifyInvalid, true
	default:
		return "", false
	}
}

// iconKey сопоставляет статус значку каталога.
func (s AddressStatus) iconKey() (MessageKey, bool) {
	switch s {
	case AddressDeliverable:
		return msgVerifyIconDeliverable, true
	case AddressRisky:
		return msgVerifyIconRisky, true
	case AddressUnknown:
		return msgVerifyIconUnknown, true
	case AddressInvalid:
		return msgVerifyIconInvalid, true
	default:
		return "", false
	}
}

// Verification — модель бейджа верификации адреса: статус, дата и источник
// проверки. Дата и источник показываются рядом со статусом (BRD §3.18); формат
// даты локализуется Formatter.
type Verification struct {
	Status AddressStatus
	Date   time.Time
	Source string
	// HasDate отличает «проверка не выполнялась» от нулевого времени.
	HasDate bool
}

// RenderVerification размечает бейдж верификации адреса в w. Подпись и значок
// берутся из каталога сообщений, а не из шаблона.
func RenderVerification(w io.Writer, verification Verification) error {
	view := verificationView{
		Class: string(verification.Status),
		Label: verification.Status.Label(),
		Icon:  verification.Status.Icon(),
		Title: verification.Status.Label(),
	}
	if !verification.Status.Valid() {
		view.Class = string(AddressUnknown)
	}
	if verification.HasDate {
		meta := DefaultMessages().Text(msgVerifyCheckedAt) + ": " + DefaultFormatter().Date(verification.Date)
		if source := verification.Source; source != "" {
			meta += " · " + source
		}
		view.Meta = meta
	}
	return renderFragment(w, "verify-badge", view)
}

// verificationView — модель разметки бейджа верификации: готовые строки, шаблон
// не содержит логики и пользовательского текста.
type verificationView struct {
	Class string
	Label string
	Icon  string
	Meta  string
	Title string
}
