package ports

import "context"

// Нормализованный словарь состояния адаптера интеграции. Консоль показывает
// оператору только состояния из этого набора; значение вне набора сводится к
// AdapterStateUnknown — состояние «в норме» не выдумывается, в том числе когда
// адаптер не сконфигурирован (пустое состояние).
const (
	// AdapterStateUp — адаптер отвечает штатно.
	AdapterStateUp = "up"
	// AdapterStateDegraded — адаптер отвечает с деградацией.
	AdapterStateDegraded = "degraded"
	// AdapterStateDown — адаптер недоступен.
	AdapterStateDown = "down"
	// AdapterStateUnknown — состояние определить нельзя.
	AdapterStateUnknown = "unknown"
)

// Нормализованный словарь причины состояния адаптера. Свободного текста здесь нет
// намеренно: произвольная строка (в том числе с токеном, ключом или внутренним
// адресом) не проходит в разметку и в сообщения об ошибках. Незнакомая причина
// сводится к AdapterReasonNone.
const (
	// AdapterReasonNone — причина не указана.
	AdapterReasonNone = ""
	// AdapterReasonUnreachable — адаптер не отвечает.
	AdapterReasonUnreachable = "unreachable"
	// AdapterReasonAuthFailed — отказ аутентификации у адаптера.
	AdapterReasonAuthFailed = "auth-failed"
	// AdapterReasonRateLimited — адаптер ограничил частоту обращений.
	AdapterReasonRateLimited = "rate-limited"
	// AdapterReasonDegraded — адаптер отвечает с деградацией.
	AdapterReasonDegraded = "degraded"
	// AdapterReasonMisconfigured — объявление адаптера некорректно.
	AdapterReasonMisconfigured = "misconfigured"
)

// AdapterStatus — элемент состояния адаптера интеграции в нормализованной форме,
// пригодной для показа оператору. Поля — строки: имя адаптера, состояние и
// нормализованная причина.
//
// Свободные каналы (State, Reason) прикладной слой сводит к закрытым словарям
// выше, поэтому секретов (токенов, ключей, учётных данных) и технических деталей
// здесь быть не может. Adapter — объявленный идентификатор адаптера, а не
// произвольный текст; разметка экранирует его при выводе.
type AdapterStatus struct {
	// Adapter — имя адаптера.
	Adapter string
	// State — состояние адаптера из словаря AdapterState*.
	State string
	// Reason — причина состояния из словаря AdapterReason*.
	Reason string
}

// AdapterCatalog — порт-интерфейс потребителя (platform-governance) для чтения
// состояния адаптеров интеграций. Владелец данных — контекст
// integration-platform: порт объявлен потребителем, а реализацию подставляет
// точка сборки, поэтому консоль не заводит собственного реестра адаптеров.
//
// nil-реализация означает, что владелец не подключён: раздел честно сообщает о
// недоступности, а не выдумывает список адаптеров.
type AdapterCatalog interface {
	// States возвращает текущее состояние адаптеров. Ошибка означает
	// недоступность источника; вызывающий отличает её через errors.Is.
	States(ctx context.Context) ([]AdapterStatus, error)
}
