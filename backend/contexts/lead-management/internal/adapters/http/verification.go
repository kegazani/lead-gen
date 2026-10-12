package httpapi

import (
	"bytes"
	"html/template"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/frontend/components"
)

// Словарь верификации адреса (web-frontend-architecture, срез 7, 7.2; BRD §3.18).
// Показ исхода верификации идёт через общий словарь frontend/components, поэтому
// консоль не заводит собственных формулировок: «доставляемый», «рискованный»,
// «неизвестно», «недействительный». Формулировка «адрес существует» запрещена —
// верификация адреса оценивает доставляемость, а не подтверждает наличие ящика.

// addressStatus сводит статус верификации домена к общему словарю доставляемости.
// Ролевой, одноразовый и известный плохой адрес — варианты риска; неизвестный
// статус не выдаётся ни за успех, ни за отказ.
func addressStatus(status domain.VerificationStatus) components.AddressStatus {
	switch status {
	case domain.StatusDeliverable:
		return components.AddressDeliverable
	case domain.StatusRisky, domain.StatusRole, domain.StatusDisposable, domain.StatusAbuse:
		return components.AddressRisky
	case domain.StatusInvalid:
		return components.AddressInvalid
	default:
		return components.AddressUnknown
	}
}

// verificationFromResult собирает модель бейджа из результата проверки домена:
// статус, дата и источник проверки.
func verificationFromResult(checked domain.CheckedEmail) components.Verification {
	return components.Verification{
		Status:  addressStatus(checked.Status),
		Date:    checked.CheckedAt,
		Source:  checked.Source,
		HasDate: !checked.CheckedAt.IsZero(),
	}
}

// verificationBadge размечает бейдж верификации через общий RenderVerification и
// возвращает готовый HTML для встраивания в полную страницу. Ошибка рендера даёт
// пустую строку: бейдж не выдаётся за успешный.
func verificationBadge(verification components.Verification) template.HTML {
	var buffer bytes.Buffer
	if err := components.RenderVerification(&buffer, verification); err != nil {
		return ""
	}
	return template.HTML(buffer.String())
}

// unverifiedBadge — исход «проверка не выполнялась»: честное «неизвестно», а не
// выдуманный статус.
func unverifiedBadge() template.HTML {
	return verificationBadge(components.Verification{Status: components.AddressUnknown})
}
