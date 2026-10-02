package ports

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// Ошибки чтения сообщества. Это ошибки порта, а не домена: они описывают
// состояние внешнего источника (сессия, доступность, ограничения), а не
// нарушение правила выжимки.
var (
	// ErrSessionRequired — авторизованная сессия отсутствует или недействительна.
	// Чтение не выполняется вовсе (spec, «Попытка чтения без авторизованной
	// сессии»).
	ErrSessionRequired = errors.New("порты: требуется авторизованная сессия")

	// ErrUnavailable — источник недоступен: сеть, прокси или отказ транспорта.
	ErrUnavailable = errors.New("порты: источник недоступен")

	// ErrCommunityUnavailable — конкретное сообщество недоступно или удалено.
	// Чтение остальных сообществ не останавливается.
	ErrCommunityUnavailable = errors.New("порты: сообщество недоступно")

	// ErrRateLimited — платформа ограничила частоту обращений.
	ErrRateLimited = errors.New("порты: превышена частота обращений")

	// ErrReadPaused — платформа потребовала приостановить обращения на срок.
	ErrReadPaused = errors.New("порты: чтение приостановлено платформой")
)

// ReadRequest — задание чтения сообщества.
type ReadRequest struct {
	// Source — разрешённый источник. Авторы и состав в домен не входят, но
	// источник нужен адаптеру, чтобы выбрать чат.
	Source domain.CommunitySource

	// Period — границы читаемых сообщений.
	Period domain.DigestPeriod

	// MaxMessages — верхняя граница числа сообщений; 0 — без ограничения.
	MaxMessages int
}

// ReadMessage — прочитанное сообщение. Поле Author принадлежит красной зоне:
// адаптер и прикладной слой его видят, домен — нет.
type ReadMessage struct {
	// MessageID — идентификатор сообщения внутри сообщества.
	MessageID domain.MessageID

	// Author — идентификатор автора (красная зона). В выжимку не попадает.
	Author string

	// Text — текст сообщения.
	Text string

	// PostedAt — время публикации.
	PostedAt time.Time
}

// ReadResult — результат чтения сообщества.
type ReadResult struct {
	// Messages — прочитанные сообщения в порядке публикации.
	Messages []ReadMessage

	// CommunitySize — размер сообщества на момент чтения. Служит порогом для
	// включения цитат.
	CommunitySize int

	// ParticipantsAvailable — доступен ли состав участников. Ложь означает
	// частичный результат: состав не подменяется оценкой (spec, «Состав
	// участников недоступен»).
	ParticipantsAvailable bool
}

// CommunityReader — порт чтения разрешённого сообщества. Владелец реализации —
// адаптер под Telegram-источник; в объёме среза это детерминированный или
// честно недоступный адаптер без сети.
type CommunityReader interface {
	// ReadCommunity читает сообщения сообщества за период. Отсутствие сессии
	// даёт ошибку, для которой истинно errors.Is(err, ErrSessionRequired);
	// недоступность сообщества — ErrCommunityUnavailable; ограничение частоты —
	// ErrRateLimited; требование паузы — ErrReadPaused.
	ReadCommunity(ctx context.Context, req ReadRequest) (ReadResult, error)
}
