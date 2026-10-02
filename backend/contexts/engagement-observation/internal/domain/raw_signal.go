package domain

import (
	"fmt"
	"strings"
	"time"
)

// RawSignal — сырой сигнал от провайдера или письма, ещё не квалифицированный в
// событие взаимодействия. Инвариант владельца: сырой сигнал не равен
// квалифицированному событию, поэтому квалификацию выполняет только
// domain.Qualify, а не сам сигнал.
//
// KindHint — подпись источника, а не вид: домен квалифицирует её сам и отвергает
// пиксельные подписи. Пикселей и метрик открытий здесь нет.
type RawSignal struct {
	// SignalID — идентификатор сигнала у источника. Пустое значение допустимо:
	// тогда ключ дедупликации выводится из содержимого сигнала.
	SignalID SignalID
	// Source — источник сигнала (провайдер, письмо, событие доставки).
	Source string
	// TenantID — тенант-владелец данных.
	TenantID TenantID
	// CompanyID — компания, к которой отнесено взаимодействие.
	CompanyID CompanyID
	// KindHint — сырая подпись вида взаимодействия.
	KindHint string
	// Outcome — сырая подпись исхода.
	Outcome string
	// LinkRef — непрозрачная ссылка трекинг-перехода (для вида transition).
	LinkRef LinkRef
	// Target — адрес назначения трекинг-перехода (для вида transition).
	Target string
	// OccurredAt — момент сигнала.
	OccurredAt time.Time
	// CorrelationID — сквозной сценарий, если источник его назвал.
	CorrelationID CorrelationID
	// CausationID — сообщение-причина, если источник его назвал.
	CausationID CausationID
}

// DedupKey возвращает ключ дедупликации входящего сигнала. Если источник назвал
// SignalID, ключ строится на нём; иначе — на содержимом сигнала. Дедупликация
// выполняется до квалификации: повтор webhook не создаёт второго события
// (architecture/domain-catalog.yaml, инвариант агрегата engagement).
func (s RawSignal) DedupKey() SignalID {
	if id := strings.TrimSpace(string(s.SignalID)); id != "" {
		return SignalID("signal:" + id)
	}
	parts := []string{
		strings.TrimSpace(string(s.TenantID)),
		strings.TrimSpace(string(s.CompanyID)),
		normalizeToken(s.KindHint),
		normalizeToken(s.Outcome),
		strings.TrimSpace(string(s.LinkRef)),
		strings.TrimSpace(s.Target),
		formatOccurred(s.OccurredAt),
	}
	sum := strings.Join(parts, "|")
	if sum == "||||||" {
		return ""
	}
	return SignalID("content:" + sum)
}

// formatOccurred возвращает устойчивое представление времени для ключа
// дедупликации: нулевое время остаётся пустым, иначе — UTC с наносекундами.
func formatOccurred(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// validate проверяет обязательные поля сырого сигнала до квалификации.
func (s RawSignal) validate() error {
	if err := s.TenantID.validate(); err != nil {
		return err
	}
	if err := s.CompanyID.validate(); err != nil {
		return err
	}
	if s.OccurredAt.IsZero() {
		return fmt.Errorf("%w: сигнал компании %s", ErrMissingTime, s.CompanyID)
	}
	return nil
}
