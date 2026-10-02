package ports

import (
	"context"
	"errors"
)

// Ошибки порта площадки. Публикация вовне — исходящее действие: отказ площадки и
// недоступность канала различимы вызывающим и не выдаются за успех.
var (
	// ErrPlatformUnavailable — официальный интерфейс площадки недоступен:
	// адаптер не подключён или вернул сбой транспорта.
	ErrPlatformUnavailable = errors.New("порты: площадка публикации недоступна")

	// ErrChannelNotReady — канал не готов: нет доступа к API, авторизация
	// недействительна или бот не является администратором.
	ErrChannelNotReady = errors.New("порты: канал площадки не готов")

	// ErrPublicationRejected — площадка отклонила материал.
	ErrPublicationRejected = errors.New("порты: площадка отклонила публикацию")
)

// PublishRequest — запрос публикации материала на площадке. Адресат —
// учётная запись платформы, поэтому персональных данных и реквизитов клиентов
// здесь нет.
type PublishRequest struct {
	// PublicationID — идентификатор публикации (ключ идемпотентности у площадки).
	PublicationID string
	// TenantID — тенант-владелец записи.
	TenantID string
	// Platform — площадка назначения (значение VO publication-target).
	Platform string
	// Account — учётная запись назначения.
	Account string
	// Title — заголовок материала (необязателен).
	Title string
	// Body — текст материала.
	Body string
}

// PublishResult — исход публикации на площадке: адрес материала.
type PublishResult struct {
	// Address — публичный адрес опубликованного материала.
	Address string
}

// PlatformPublisher — порт публикации вовне.
//
// Реализация ходит в официальный API площадки (или в интеграционную платформу,
// дающую его за provider-port). Контекст не знает, каким транспортом получен
// доступ: он требует лишь, чтобы способ был официальным, а вызов — идемпотентным
// по PublicationID. Автоматизация сессии и браузера сюда не помещается.
type PlatformPublisher interface {
	// Ready проверяет готовность канала до формирования материала. Отсутствие
	// доступа даёт ошибку, для которой истинно errors.Is(err, ErrChannelNotReady).
	Ready(ctx context.Context, platform, account string) error

	// Publish публикует материал через официальный интерфейс площадки. Отказ
	// площадки — ErrPublicationRejected, сбой транспорта — ErrPlatformUnavailable.
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}
