package app

// ErrorCode — стабильный машинный код отказа задания поиска. Коды не меняются
// задним числом: по ним будущая админка агрегирует отказы.
type ErrorCode string

const (
	// ErrorCodePayloadInvalid — некорректный или нечитаемый запрос задания.
	ErrorCodePayloadInvalid ErrorCode = "payload.invalid"
	// ErrorCodeBudgetExhausted — исчерпан бюджет кредитов Firecrawl.
	ErrorCodeBudgetExhausted ErrorCode = "budget.exhausted"
	// ErrorCodeSearchFailed — отказ поискового движка.
	ErrorCodeSearchFailed ErrorCode = "search.failed"
	// ErrorCodeCrawlFailed — отказ движка обхода.
	ErrorCodeCrawlFailed ErrorCode = "crawl.failed"
	// ErrorCodeParseFailed — страница не прошла разбор.
	ErrorCodeParseFailed ErrorCode = "parse.failed"
	// ErrorCodeInterrupted — задание прервано остановкой процесса (перезапуском)
	// и исчерпало допустимое число попыток. Отличается от отказа движка.
	ErrorCodeInterrupted ErrorCode = "lifecycle.interrupted"
)

// Message возвращает бизнес-формулировку для кода — то, что видит оператор в
// журнале, без технических деталей. Доступна и вне пакета: политику отказа для
// прерванных заданий задаёт сборка сервиса.
func (c ErrorCode) Message() string {
	switch c {
	case ErrorCodePayloadInvalid:
		return "Некорректный запрос"
	case ErrorCodeBudgetExhausted:
		return "Закончились кредиты"
	case ErrorCodeSearchFailed:
		return "Не удалось выполнить поиск"
	case ErrorCodeCrawlFailed:
		return "Не удалось обойти сайт"
	case ErrorCodeParseFailed:
		return "Страница не распознана"
	case ErrorCodeInterrupted:
		return "Задание прервано перезапуском"
	default:
		return "Ошибка задания"
	}
}

// SearchJobError — ошибка задания поиска, несущая стабильный код, бизнес-
// формулировку и техническую деталь. Реализует jobs.CodedError: примитив достаёт
// код и сообщение, а Error() (техническая деталь) уходит только в лог.
type SearchJobError struct {
	code   ErrorCode
	detail error
}

// newSearchJobError оборачивает техническую деталь кодом отказа; бизнес-
// формулировка выводится из кода.
func newSearchJobError(code ErrorCode, detail error) *SearchJobError {
	return &SearchJobError{code: code, detail: detail}
}

// Error возвращает техническую деталь (для лога).
func (e *SearchJobError) Error() string {
	if e.detail != nil {
		return e.detail.Error()
	}
	return string(e.code)
}

// Unwrap раскрывает техническую деталь для errors.Is/As.
func (e *SearchJobError) Unwrap() error { return e.detail }

// Code реализует jobs.CodedError.
func (e *SearchJobError) Code() string { return string(e.code) }

// Message реализует jobs.CodedError.
func (e *SearchJobError) Message() string { return e.code.Message() }
