// Package adapterstatus — опубликованный контракт adapter-status контекста
// integration-platform.
//
// Это синхронный запросный контракт: потребитель (platform-governance, раздел
// интеграций консоли) читает состояние объявленных адаптеров интеграционной
// платформы, а владелец отдаёт по каждому адаптеру имя, состояние и
// нормализованную причину. Транспорт задаётся топологией — прямой вызов в одном
// процессе или сетевой http-json (пакет adapterstatus/httpport); форма контракта
// от транспорта не зависит.
//
// # Безопасность
//
// Поля Status — строки из закрытых словарей владельца (up/degraded/down/unknown и
// коды причин). Свободного текста в контракте нет, поэтому секреты (ключи,
// токены, учётные данные) не могут попасть в отдаваемые данные по построению:
// владелец нормализует значение до отправки, а потребитель сводит неизвестное к
// «unknown».
package adapterstatus

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта.
const ContractName = "adapter-status"

// SchemaVersion — текущая версия схемы контракта (semver). Изменять по правилам
// semver из envelope.go контрактов контекста.
const SchemaVersion = "1.1.0"

// Вид и транспорт контракта. Вид фиксирован: adapter-status — запрос (query), а
// не команда или событие. Транспорт синхронный: sync в одном процессе, http-json
// при разделении сервисов по процессам.
const (
	// KindQuery — вид сообщения: запрос.
	KindQuery = "query"
	// TransportSync — прямой вызов в одном процессе.
	TransportSync = "sync"
	// TransportHTTPJSON — синхронный вызов сетевым JSON/HTTP-клиентом.
	TransportHTTPJSON = "http-json"
)

// Нормализованный словарь состояния адаптера. Набор совпадает со словарём
// потребителя (platform-governance): состояние вне набора потребитель сводит к
// unknown, состояние «в норме» не выдумывается.
const (
	// StateUp — адаптер отвечает штатно.
	StateUp = "up"
	// StateDegraded — адаптер отвечает с деградацией.
	StateDegraded = "degraded"
	// StateDown — адаптер недоступен.
	StateDown = "down"
	// StateUnknown — состояние определить нельзя.
	StateUnknown = "unknown"
)

// Нормализованный словарь причины состояния. Свободного текста нет: произвольная
// строка (в том числе с токеном или внутренним адресом) в контракт не попадает.
const (
	// ReasonNone — причина не указана.
	ReasonNone = ""
	// ReasonUnreachable — адаптер не отвечает.
	ReasonUnreachable = "unreachable"
	// ReasonAuthFailed — отказ аутентификации у адаптера.
	ReasonAuthFailed = "auth-failed"
	// ReasonRateLimited — адаптер ограничил частоту обращений.
	ReasonRateLimited = "rate-limited"
	// ReasonDegraded — адаптер отвечает с деградацией.
	ReasonDegraded = "degraded"
	// ReasonMisconfigured — объявление адаптера некорректно.
	ReasonMisconfigured = "misconfigured"
	// ReasonProbeNotConfigured — доступность настроенного адаптера не проверялась.
	ReasonProbeNotConfigured = "probe_not_configured"
)

// ErrUnavailable — источник состояния адаптеров недоступен: владелец не
// подключён, сбой транспорта или нераспознанный ответ. Пустой список не
// подменяется успехом: недоступность и «адаптеров нет» различимы.
var ErrUnavailable = errors.New("adapter-status: источник состояния адаптеров недоступен")

// Status — состояние одного адаптера. Adapter — объявленное имя адаптера (не
// секрет); State и Reason — значения закрытых словарей. Поля — строки, чтобы
// контракт не зависел от типов владельца.
type Status struct {
	// Adapter — стабильное имя адаптера.
	Adapter string `json:"adapter"`
	// State — состояние из набора State*.
	State string `json:"state"`
	// Reason — код причины из набора Reason*. Пусто — причина не указана.
	Reason string `json:"reason,omitempty"`
}

// Response — тело ответа владельца на запрос состояния адаптеров. Включает
// имя контракта и версию схемы, чтобы потребитель мог отвергнуть несовместимую
// версию.
type Response struct {
	// Contract — имя контракта; равно ContractName.
	Contract string `json:"contract"`
	// SchemaVersion — версия схемы контракта в формате semver.
	SchemaVersion string `json:"schema_version"`
	// Adapters — состояние объявленных адаптеров в порядке отображения.
	Adapters []Status `json:"adapters"`
}

// Port — запросный контракт adapter-status. Реализация владельца отдаёт
// состояние объявленных адаптеров; ошибка означает недоступность источника, а не
// пустой список.
type Port interface {
	// Statuses возвращает состояние адаптеров. Пустой срез — «адаптеров не
	// объявлено», ошибка — недоступность владельца.
	Statuses(ctx context.Context) ([]Status, error)
}
