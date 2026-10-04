// Package deliverytarget — опубликованный (published language) контракт
// delivery-target контекста lead-management: разрешение непрозрачной ссылки на
// получателя (`RecipientRef`) в проверенный адрес назначения по тенанту.
//
// Владелец адреса — lead-management (architecture/domain-catalog.yaml): контакты
// (`contact`) принадлежат ему, а `approved-action` намеренно не несёт адресов и
// персональных данных. Потребитель (мост `delivery-bridge` контекста
// message-delivery) держит только эту форму и получает адрес в момент доставки,
// не поднимая PII в утверждённое действие (связь `leads-for-delivery`).
//
// Зависимости — только стандартная библиотека. Владелец реализует Resolver, а
// потребитель зависит от контракта, не видя реализации.
//
// Приватность и честность. Неизвестная или чужая ссылка — доменный отказ
// (`ErrRejected`), недоступность владельца — сбой (`ErrUnavailable`); адрес
// никогда не выдумывается. Изоляция тенанта — на стороне владельца: резолвер
// отвечает только за контур своего тенанта.
//
// Транспорт — sync/http-json (query): при co-location вызов прямой (InProc),
// при разделении сервисов — сетевой клиент той же формы; выбирает точка сборки
// по топологии.
package deliverytarget

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "delivery-target"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// ErrRejected — ссылка на получателя отклонена по доменной причине владельца:
// неизвестная или чужая (принадлежащая другому тенанту) ссылка, либо контакт без
// пригодного адреса. Потребитель различает этот отказ от сбоя транспорта и не
// подставляет адрес сам.
var ErrRejected = errors.New("deliverytarget: ссылка на получателя отклонена владельцем")

// ErrUnavailable — владелец адреса недоступен: контракт не подключён, резолвер не
// привязан к тенанту или сбой транспорта/чтения. Потребитель не выдаёт это за
// разрешённый адрес.
var ErrUnavailable = errors.New("deliverytarget: владелец получателя недоступен")

// Recipient — разрешённый получатель: единственное, что нужно доставке, —
// проверенный адрес назначения. Других персональных данных контракт не несёт.
type Recipient struct {
	// Address — проверенный адрес назначения. Единственное поле с адресом:
	// владелец выдаёт его только из собственного атрибута контакта.
	Address string
}

// Resolver — запрос владельцу на разрешение непрозрачной ссылки получателя
// (`recipientRef`) в проверенный адрес назначения в контуре тенанта `tenantID`.
// Реализация обязана соблюдать изоляцию тенанта: чужая ссылка не разрешается.
// ErrRejected — доменный отказ, ErrUnavailable — недоступность владельца.
type Resolver interface {
	ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (Recipient, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// ResolveFunc реализует разрешение ссылки получателя.
	ResolveFunc func(ctx context.Context, tenantID, recipientRef string) (Recipient, error)
}

// ResolveRecipient реализует Resolver. Незаданная функция честно сообщает о
// недоступности владельца, а не выдумывает адрес.
func (h InProcHandler) ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (Recipient, error) {
	if h.ResolveFunc == nil {
		return Recipient{}, ErrUnavailable
	}
	return h.ResolveFunc(ctx, tenantID, recipientRef)
}

// InProc заворачивает обработчик в Resolver для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Resolver { return handler }
