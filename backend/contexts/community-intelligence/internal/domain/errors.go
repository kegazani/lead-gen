package domain

import "errors"

// Доменные ошибки контекста community-intelligence.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingTime — факт домена не назвал время.
	ErrMissingTime = errors.New("домен: отсутствует время")

	// ErrMissingEventID — событие домена без идентификатора: его нельзя
	// дедуплицировать у потребителя.
	ErrMissingEventID = errors.New("домен: событие без идентификатора")

	// ErrSourceNotAuthorized — сообщество не разрешено к чтению. Инвариант
	// «читаются только разрешённые сообщества» (policy authorized-community-read).
	ErrSourceNotAuthorized = errors.New("домен: сообщество не разрешено к чтению")

	// ErrBotSourceRejected — источником указан бот. Bot API не даёт ни истории,
	// ни состава участников (spec, Решение 1): такая конфигурация отклоняется.
	ErrBotSourceRejected = errors.New("домен: чтение через бота недопустимо")

	// ErrUnknownSourceKind — вид источника не объявлен доменом.
	ErrUnknownSourceKind = errors.New("домен: неизвестный вид источника")

	// ErrEmptySourceTitle — у источника пустое название.
	ErrEmptySourceTitle = errors.New("домен: пустое название сообщества")

	// ErrInvalidPeriod — границы периода некорректны (нулевые или конец раньше
	// начала).
	ErrInvalidPeriod = errors.New("домен: некорректный период выжимки")

	// ErrNoMessages — за период не найдено ни одного пригодного сообщения.
	// Пустая выжимка с нулями не формируется (spec, «Сообщений за период нет»).
	ErrNoMessages = errors.New("домен: за период нет сообщений")

	// ErrEmptyTopic — тема без названия.
	ErrEmptyTopic = errors.New("домен: пустая тема выжимки")

	// ErrInvalidMentions — число упоминаний темы не положительно.
	ErrInvalidMentions = errors.New("домен: некорректное число упоминаний")

	// ErrEmptyQuote — цитата пуста после обезличивания.
	ErrEmptyQuote = errors.New("домен: пустая цитата")

	// ErrInvalidSnapshot — сохранённое состояние внутренне противоречиво и не
	// может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние выжимки")
)
