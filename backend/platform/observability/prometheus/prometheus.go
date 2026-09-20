// Package prometheus — технический примитив чтения метрик из HTTP API
// Prometheus. Пакет знает только протокол самого API и не содержит доменных
// терминов: он превращает ответы `/api/v1/query` и `/api/v1/query_range` в
// нейтральные временные ряды.
//
// Пакет отвечает только за транспорт и разбор: таймауты и отмена приходят из
// ctx, повторов внутри нет — повтор идемпотентных запросов остаётся заботой
// вызывающего (platform/reliability/resilience). Клиент потокобезопасен: после
// NewClient состояние не изменяется, а RoundTripper разделяется между
// вызовами так же, как это делает net/http.
//
// Осознанно не поддержано:
//   - resultType `scalar` и `string`: консоль топологии читает временные ряды,
//     а одиночные значения и строки не выражают серию; такой ответ — ошибка, а
//     не молчаливое приведение;
//   - потоковые (streaming/chunked) ответы и POST-форма запроса;
//   - аутентификация и TLS-настройка: они задаются не этим примитивом, а
//     транспортом, который сюда передают.
package prometheus

import "time"

// Sample — одна точка временного ряда: момент времени и значение.
type Sample struct {
	// Time — момент измерения в UTC.
	Time  time.Time
	Value float64
}

// Series — временной ряд с метками. Metric — набор меток серии в том виде, в
// каком их вернул Prometheus (включая `__name__`).
type Series struct {
	Metric  map[string]string
	Samples []Sample
}

// Vector — результат instant-запроса (`query`). У каждой серии ровно одна
// точка: instant-запрос возвращает значение на момент вычисления.
type Vector []Series

// Matrix — результат range-запроса (`query_range`). У каждой серии набор точек
// в порядке возрастания времени.
type Matrix []Series

// APIError — ошибка уровня протокола Prometheus: не-2xx ответ HTTP или
// `status:"error"` в теле. Сетевые сбои и некорректный JSON сюда не попадают —
// это обычные обёрнутые ошибки.
type APIError struct {
	// Status — код ответа HTTP в десятичном виде.
	Status string
	// ErrorType — классификация ошибки Prometheus (например, `bad_data`).
	ErrorType string
	// Message — человекочитаемое описание от Prometheus (поле `error` в ответе).
	// Названо Message, а не Error: тип обязан реализовать error методом
	// Error(), а поле и метод с одним именем в Go сосуществовать не могут.
	Message string
}

// Error реализует error.
func (e *APIError) Error() string {
	msg := "prometheus: API вернул ошибку"
	if e.Status != "" {
		msg += " (HTTP " + e.Status + ")"
	}
	switch {
	case e.ErrorType != "" && e.Message != "":
		msg += ": " + e.ErrorType + ": " + e.Message
	case e.Message != "":
		msg += ": " + e.Message
	case e.ErrorType != "":
		msg += ": " + e.ErrorType
	}
	return msg
}

const (
	// queryPath и queryRangePath — пути HTTP API Prometheus.
	queryPath      = "/api/v1/query"
	queryRangePath = "/api/v1/query_range"

	// statusSuccess — единственный статус успешного ответа.
	statusSuccess = "success"
	// resultTypeVector и resultTypeMatrix — поддержанные виды результата.
	resultTypeVector = "vector"
	resultTypeMatrix = "matrix"

	// maxResponseBytes ограничивает объём читаемого ответа, чтобы гигантский
	// ответ не съел память процесса. При превышении возвращается ошибка.
	maxResponseBytes = 8 << 20 // 8 МиБ
)
