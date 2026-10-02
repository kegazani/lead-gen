package domain

import "fmt"

// ApprovedActionOnly — политика approved-action-only: сообщение создаётся только
// для утверждённого действия.
//
// Чистая функция без побочных эффектов: проверяет, что команда несёт ссылку на
// санкционировавшее её решение. Знание о том, что действие действительно
// утверждено, приходит извне (contract approved-action); домен требует лишь
// наличия санкции, без которой отправка недопустима.
func ApprovedActionOnly(cmd DeliveryCommand) error {
	if err := cmd.ApprovedActionID.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrApprovedActionRequired, err)
	}
	return nil
}

// DeliverabilityDecision — результат политики deliverability-policy.
type DeliverabilityDecision struct {
	// Allowed — допустима ли попытка доставки.
	Allowed bool

	// Reason — основание решения в терминах домена.
	Reason string

	// Suspended — отказ вызван активным ограничением репутации домена.
	Suspended bool

	// Suppressed — отказ вызван активным стоп-листом получателя.
	Suppressed bool
}

// DeliverabilityPolicy — политика deliverability-policy: решает, допустима ли
// попытка доставки по команде с учётом репутации отправляющего домена и
// стоп-листа (delivery-command → message-delivered).
//
// Чистая функция: не читает время и не обращается к хранилищу — репутация и
// признак стоп-листа приходят аргументами. Порядок проверок: сначала форма
// команды и наличие утверждённого действия, затем стоп-лист (блокирует попытку
// непосредственно перед доставкой), затем репутация домена.
func DeliverabilityPolicy(cmd DeliveryCommand, reputation *SenderReputation, suppressed bool) (DeliverabilityDecision, error) {
	if err := ApprovedActionOnly(cmd); err != nil {
		return DeliverabilityDecision{}, err
	}
	if err := cmd.validate(); err != nil {
		return DeliverabilityDecision{}, err
	}
	if suppressed {
		return DeliverabilityDecision{
			Allowed:    false,
			Suppressed: true,
			Reason:     "активный стоп-лист блокирует попытку доставки",
		}, nil
	}
	if reputation != nil && reputation.Suspended() {
		return DeliverabilityDecision{
			Allowed:   false,
			Suspended: true,
			Reason:    "активные ограничения репутации приостанавливают отправки домена",
		}, nil
	}
	return DeliverabilityDecision{Allowed: true, Reason: "отправка допустима"}, nil
}
