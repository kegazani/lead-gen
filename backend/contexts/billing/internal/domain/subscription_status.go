package domain

import "fmt"

// SubscriptionStatus — статус подписки.
//
// Статусная модель: active ⇄ suspended. active означает оплаченный период и
// доступ в объёме тарифа; suspended — приостановку доступа с сохранением данных
// клиента (BRD, приостановка и возобновление).
type SubscriptionStatus string

const (
	// StatusActive — подписка активна: доступ в объёме тарифа.
	StatusActive SubscriptionStatus = "active"

	// StatusSuspended — подписка приостановлена: доступ ограничен, данные сохранены.
	StatusSuspended SubscriptionStatus = "suspended"
)

// SubscriptionStatuses возвращает согласованный набор статусов подписки.
func SubscriptionStatuses() []SubscriptionStatus {
	return []SubscriptionStatus{StatusActive, StatusSuspended}
}

// IsActive сообщает, что подписка активна.
func (s SubscriptionStatus) IsActive() bool { return s == StatusActive }

// isValid сообщает, что статус объявлен доменом.
func (s SubscriptionStatus) isValid() bool {
	return s == StatusActive || s == StatusSuspended
}

// validate отвергает статус вне согласованного набора.
func (s SubscriptionStatus) validate() error {
	if !s.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownSubscriptionStatus, s)
	}
	return nil
}

// String возвращает статус.
func (s SubscriptionStatus) String() string { return string(s) }
