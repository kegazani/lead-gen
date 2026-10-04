package domain

import (
	"fmt"
	"net/url"
	"time"
)

// DeliveryCommand — доменное представление входящей команды контракта
// delivery-command: доставить утверждённое почтовое действие.
//
// Команда уже очищена на границе контекста: адрес разобран в value object,
// отправляющий домен нормализован. Поле ApprovedActionID обязательно —
// инвариант «сообщение создаётся только для утверждённого действия».
type DeliveryCommand struct {
	CommandID        CommandID
	TenantID         TenantID
	ApprovedActionID ApprovedActionID
	RecipientRef     RecipientRef
	Address          DeliveryAddress
	SenderDomain     SenderDomain
	// Subject и Body — содержимое письма, переданное вызывающим. Реальному
	// провайдеру нужны тема и тело; без них отправка возможна лишь как честный
	// отказ. Материал разрешается адаптером контракта content-version до сборки
	// команды: домен содержимое не выдумывает.
	Subject       string
	Body          string
	CorrelationID CorrelationID
	CausationID   string
	RequestedAt   time.Time
}

// validate проверяет обязательные части команды. Канал в доменной команде
// отсутствует: контекст обслуживает только email (capability
// outreach-channel-email), а выбор канала уже сделан на границе.
func (c DeliveryCommand) validate() error {
	if err := c.CommandID.validate(); err != nil {
		return err
	}
	if err := c.TenantID.validate(); err != nil {
		return err
	}
	if err := c.ApprovedActionID.validate(); err != nil {
		return err
	}
	if err := c.RecipientRef.validate(); err != nil {
		return err
	}
	if err := c.Address.validate(); err != nil {
		return err
	}
	return c.SenderDomain.validate()
}

// IdempotencyKey возвращает ключ идемпотентности команды: (tenant_id,
// approved_action_id). Повторная отправка того же утверждённого действия не
// создаёт второго сообщения и второго внешнего эффекта. Компоненты кодируются,
// чтобы разделитель не допускал коллизий.
func (c DeliveryCommand) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s", url.QueryEscape(string(c.TenantID)), url.QueryEscape(string(c.ApprovedActionID)))
}
