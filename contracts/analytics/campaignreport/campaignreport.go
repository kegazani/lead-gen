// Package campaignreport — опубликованный контракт campaign-report контекста
// analytics в части агрегатов процесса для эксплуатационной консоли.
//
// Это синхронный запросный контракт: потребитель (platform-governance, экран
// операций /operations-cockpit) читает у владельца analytics обезличенные цифры
// процесса (найдено/обработано/отклонено/в работе/результат) с разбивкой по
// клиенту и по времени, а также агрегаты по клиентам для полноэкранного
// дашборда с кодами выявленных аномалий. Владелец данных — analytics: он строит
// агрегаты из своих read-моделей (campaign-reporting, lead-quality-insights,
// проекции) и отдаёт только сводные счётчики.
//
// # Приватность
//
// Контракт несёт только агрегаты и обезличенные признаки: персональных данных и
// содержимого красной зоны здесь нет по построению. Срез ниже порога минимальной
// группы владелец не отдаёт (клиент отсутствует в ответе), поэтому срез из одной
// записи не раскрывает личность. Неизвестное состояние не подменяется нулём:
// недоступность владельца возвращается ошибкой (ErrUnavailable), а не пустым
// успехом.
//
// # Транспорт
//
// Форма контракта не зависит от транспорта: при co-location вызов прямой
// (InProc), при разделении сервисов — сетевой клиент (пакет campaignreport/
// httpport). Форма wire — snake_case (json-теги ниже), одинаковая у владельца и
// клиента.
//
// Зависимости — только стандартная библиотека.
package campaignreport

import (
	"context"
	"errors"
	"time"
)

// ContractName — имя публикуемого контракта.
const ContractName = "campaign-report"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// Вид и транспорт контракта. Вид фиксирован: campaign-report — запрос (query), а
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

// Категории процесса. Набор совпадает с категориями цифр экрана операций
// (platform-governance): найдено, обработано, отклонено, в работе, результат.
// Значения — машинные коды; потребитель переводит их в подписи.
const (
	// CategoryFound — найдено компаний.
	CategoryFound = "found"
	// CategoryProcessed — обработано компаний (учтён исход классификации).
	CategoryProcessed = "processed"
	// CategoryRejected — отклонено компаний (вердикт «не подходит»).
	CategoryRejected = "rejected"
	// CategoryInProgress — в работе сейчас.
	CategoryInProgress = "in_progress"
	// CategoryResult — результат процесса (ответили).
	CategoryResult = "result"
)

// Коды выявленных аномалий агрегата клиента. Значения — машинные коды закрытого
// словаря; свободного текста контракт не несёт.
const (
	// AnomalyRejectionSpike — устойчиво высокая доля отклонённых.
	AnomalyRejectionSpike = "rejection_spike"
	// AnomalyStalled — накопившаяся очередь без ответа (процесс замер).
	AnomalyStalled = "stalled"
)

// ErrUnavailable — источник агрегатов недоступен: владелец не подключён, сбой
// транспорта или нераспознанный ответ. Потребитель показывает честное
// «недоступно», а не нули и не пустой успех.
var ErrUnavailable = errors.New("campaign-report: агрегаты недоступны")

// ErrRejected — запрос отклонён по доменной причине владельца (некорректные
// поля). Потребитель различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("campaign-report: запрос отклонён владельцем")

// Request — запрос агрегатов процесса.
type Request struct {
	// Window — окно разбивки по времени: ряд периодов ограничивается интервалом
	// [now-Window, now]. Счётчики «найдено»/«в работе»/«результат» — текущие
	// сводные значения и окном не фильтруются (у источника нет среза во времени).
	// <= 0 — окно владельца по умолчанию.
	Window time.Duration
	// Actor — инициатор чтения (для трассировки отказа владельцем). Может быть
	// пустым: контракт не требует актора для агрегатов без персональных данных.
	Actor string
}

// Validate проверяет корректность запроса.
func (r Request) Validate() error {
	if r.Window < 0 {
		return errors.New("campaignreport: некорректное поле: window")
	}
	return nil
}

// ClientNumbers — цифры процесса по одному клиенту. Только обезличенные счётчики.
type ClientNumbers struct {
	// ClientID — идентификатор клиента (тенанта); персональных данных не несёт.
	ClientID string `json:"client_id"`
	// Found — найдено компаний.
	Found int `json:"found"`
	// Processed — обработано компаний (учтён исход классификации).
	Processed int `json:"processed"`
	// Rejected — отклонено компаний (вердикт «не подходит»).
	Rejected int `json:"rejected"`
	// InProgress — в работе сейчас.
	InProgress int `json:"in_progress"`
	// Result — результат процесса (ответили).
	Result int `json:"result"`
}

// PeriodEntry — цифры процесса за один временной интервал (разбивка по времени).
// Интервал — день; значения — сводные по всем клиентам, прошедшим порог
// минимальной группы. Представлены только категории, у которых есть временное
// измерение в источнике: найдено (рост по дням) и обработано/отклонено (исходы
// классификации по дням). «В работе» и «результат» — точечные счётчики без
// временного ряда, поэтому в разбивку не входят, а не подставляются нулём.
type PeriodEntry struct {
	// Date — день интервала в формате YYYY-MM-DD.
	Date string `json:"date"`
	// Found — найдено компаний за день.
	Found int `json:"found"`
	// Processed — обработано компаний за день.
	Processed int `json:"processed"`
	// Rejected — отклонено компаний за день.
	Rejected int `json:"rejected"`
}

// ClientAggregate — агрегат по клиенту для полноэкранного дашборда: те же
// счётчики плюс коды выявленных аномалий.
type ClientAggregate struct {
	ClientID   string   `json:"client_id"`
	Found      int      `json:"found"`
	Processed  int      `json:"processed"`
	Rejected   int      `json:"rejected"`
	InProgress int      `json:"in_progress"`
	Result     int      `json:"result"`
	Anomalies  []string `json:"anomalies"`
}

// NumbersResponse — ответ на запрос цифр процесса: срез по клиентам и разбивка
// по времени. Включает имя контракта и версию схемы, чтобы потребитель мог
// отвергнуть несовместимую версию.
type NumbersResponse struct {
	Contract      string          `json:"contract"`
	SchemaVersion string          `json:"schema_version"`
	Clients       []ClientNumbers `json:"clients"`
	Periods       []PeriodEntry   `json:"periods"`
	Count         int             `json:"count"`
}

// ClientsResponse — ответ на запрос агрегатов по клиентам для дашборда.
type ClientsResponse struct {
	Contract      string            `json:"contract"`
	SchemaVersion string            `json:"schema_version"`
	Clients       []ClientAggregate `json:"clients"`
	Count         int               `json:"count"`
}

// Port — запросный контракт campaign-report. Реализация владельца отдаёт
// агрегаты процесса; ошибка означает недоступность источника, а не пустой срез.
type Port interface {
	// Numbers возвращает цифры процесса по клиентам и разбивку по времени.
	Numbers(ctx context.Context, request Request) (NumbersResponse, error)
	// Clients возвращает агрегаты по клиентам для полноэкранного дашборда.
	Clients(ctx context.Context, request Request) (ClientsResponse, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Незаданная функция честно сообщает о недоступности владельца, а не выдумывает
// результат.
type InProcHandler struct {
	NumbersFunc func(ctx context.Context, request Request) (NumbersResponse, error)
	ClientsFunc func(ctx context.Context, request Request) (ClientsResponse, error)
}

// Numbers реализует Port.
func (h InProcHandler) Numbers(ctx context.Context, request Request) (NumbersResponse, error) {
	if h.NumbersFunc == nil {
		return NumbersResponse{}, ErrUnavailable
	}
	return h.NumbersFunc(ctx, request)
}

// Clients реализует Port.
func (h InProcHandler) Clients(ctx context.Context, request Request) (ClientsResponse, error) {
	if h.ClientsFunc == nil {
		return ClientsResponse{}, ErrUnavailable
	}
	return h.ClientsFunc(ctx, request)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
