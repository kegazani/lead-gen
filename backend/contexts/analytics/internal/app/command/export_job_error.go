package command

// ErrorCode — стабильный машинный код отказа задания выгрузки отчёта. Коды не
// меняются задним числом: по ним оператор отличает исходы, не читая технических
// деталей.
type ErrorCode string

const (
	// ErrorCodePayloadInvalid — некорректный или нечитаемый запрос задания.
	ErrorCodePayloadInvalid ErrorCode = "export.payload.invalid"
	// ErrorCodeReportUnavailable — контур отчёта недоступен на момент сборки файла.
	ErrorCodeReportUnavailable ErrorCode = "export.report.unavailable"
	// ErrorCodeRenderFailed — файл выгрузки не собрался.
	ErrorCodeRenderFailed ErrorCode = "export.render.failed"
	// ErrorCodeResultStoreFailed — готовый файл не удалось сохранить.
	ErrorCodeResultStoreFailed ErrorCode = "export.result.store.failed"
	// ErrorCodeInterrupted — задание прервано остановкой процесса (перезапуском)
	// и исчерпало допустимое число попыток.
	ErrorCodeInterrupted ErrorCode = "export.interrupted"
)

// Message возвращает бизнес-формулировку для кода — то, что видит оператор, без
// технических деталей.
func (c ErrorCode) Message() string {
	switch c {
	case ErrorCodePayloadInvalid:
		return "Некорректный запрос выгрузки"
	case ErrorCodeReportUnavailable:
		return "Отчёт недоступен: контур клиента не отвечает"
	case ErrorCodeRenderFailed:
		return "Не удалось собрать файл выгрузки"
	case ErrorCodeResultStoreFailed:
		return "Не удалось сохранить результат выгрузки"
	case ErrorCodeInterrupted:
		return "Выгрузка прервана перезапуском"
	default:
		return "Ошибка выгрузки"
	}
}

// ExportJobError — ошибка задания выгрузки, несущая стабильный код, бизнес-
// формулировку и техническую деталь. Реализует jobs.CodedError: примитив достаёт
// код и сообщение, а Error() (техническая деталь) уходит только в лог.
type ExportJobError struct {
	code   ErrorCode
	detail error
}

// newExportJobError оборачивает техническую деталь кодом отказа; бизнес-
// формулировка выводится из кода.
func newExportJobError(code ErrorCode, detail error) *ExportJobError {
	return &ExportJobError{code: code, detail: detail}
}

// Error возвращает техническую деталь (для лога).
func (e *ExportJobError) Error() string {
	if e.detail != nil {
		return e.detail.Error()
	}
	return string(e.code)
}

// Unwrap раскрывает техническую деталь для errors.Is/As.
func (e *ExportJobError) Unwrap() error { return e.detail }

// Code реализует jobs.CodedError.
func (e *ExportJobError) Code() string { return string(e.code) }

// Message реализует jobs.CodedError.
func (e *ExportJobError) Message() string { return e.code.Message() }
