package ports

import "context"

// AdapterHealth — состояние адаптера интеграции из фиксированного набора.
type AdapterHealth string

const (
	// AdapterHealthy — адаптер отвечает штатно.
	AdapterHealthy AdapterHealth = "up"
	// AdapterDegraded — адаптер отвечает с деградацией.
	AdapterDegraded AdapterHealth = "degraded"
	// AdapterDown — адаптер недоступен.
	AdapterDown AdapterHealth = "down"
	// AdapterUnknown — состояние определить нельзя: консоль не подставляет «в норме».
	AdapterUnknown AdapterHealth = "unknown"
)

// AdapterReason — нормализованная причина состояния адаптера из закрытого
// набора. Свободного текста здесь нет намеренно: произвольная строка (в том
// числе с токеном или внутренним адресом) не может попасть в разметку по типу.
// Незнакомая причина сводится к пустой. Причину называет владелец адаптеров.
type AdapterReason string

const (
	// AdapterReasonNone — причина не указана.
	AdapterReasonNone AdapterReason = ""
	// AdapterReasonUnreachable — адаптер не отвечает.
	AdapterReasonUnreachable AdapterReason = "unreachable"
	// AdapterReasonAuthFailed — отказ аутентификации у адаптера.
	AdapterReasonAuthFailed AdapterReason = "auth-failed"
	// AdapterReasonRateLimited — адаптер ограничил частоту обращений.
	AdapterReasonRateLimited AdapterReason = "rate-limited"
	// AdapterReasonDegraded — адаптер отвечает с деградацией.
	AdapterReasonDegraded AdapterReason = "degraded"
	// AdapterReasonMisconfigured — объявление адаптера некорректно.
	AdapterReasonMisconfigured AdapterReason = "misconfigured"
)

// AdapterState — нормализованное состояние адаптера интеграции для раздела
// «Интеграции» консоли. Поля суть только то, что можно показывать оператору:
// имя адаптера, версия набора возможностей, состояние и нормализованная причина.
// Секретов (токенов, ключей, учётных данных) здесь быть не может по построению.
type AdapterState struct {
	// Name — имя адаптера.
	Name string
	// CapabilitySetVersion — версия набора возможностей (semver); пусто, если не
	// объявлена.
	CapabilitySetVersion string
	// Health — состояние адаптера.
	Health AdapterHealth
	// Reason — нормализованная причина состояния.
	Reason AdapterReason
}

// AdapterCatalog — порт чтения состояния адаптеров интеграций. Владелец
// адаптеров — контекст integration-platform; реализацию подставляет точка
// сборки. nil-реализация означает, что владелец не подключён: раздел честно
// сообщает о недоступности, а не выдумывает список адаптеров.
type AdapterCatalog interface {
	// States возвращает текущее состояние адаптеров. Ошибка означает
	// недоступность источника; вызывающий отличает её через errors.Is.
	States(ctx context.Context) ([]AdapterState, error)
}
