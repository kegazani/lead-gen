// Package publicationcommand объявляет опубликованный контракт
// publication-command контекста publishing: команда на публикацию утверждённого
// материала.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель держит
// только эту форму и не импортирует внутренние пакеты контекста-владельца.
// Публикуется только утверждённый материал-производная от выжимки; сырых
// сообщений сообщества и данных их авторов команда не несёт.
//
// Зависимости — исключительно стандартная библиотека. Транспорт — sync/http-json:
// при co-location вызов прямой (InProc), при разделении сервисов — сетевой клиент;
// выбирает точка сборки по топологии.
package publicationcommand

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "publication-command"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// Имена площадок. Значения совпадают с доменным VO publication-target владельца:
// контракт не принимает площадку, которой нет в домене. Набор закрыт.
const (
	// PlatformDzenName — Дзен.
	PlatformDzenName = "dzen"
	// PlatformLinkedInName — LinkedIn.
	PlatformLinkedInName = "linkedin"
	// PlatformTelegramName — сообщество Telegram.
	PlatformTelegramName = "telegram"
)

var allowedPlatforms = map[string]struct{}{
	PlatformDzenName:     {},
	PlatformLinkedInName: {},
	PlatformTelegramName: {},
}

// Ошибки контракта. Потребитель различает доменный отказ владельца и сбой
// транспорта.
var (
	// ErrRejected — команда отклонена владельцем по доменной причине (например,
	// отсутствует выжимка-источник).
	ErrRejected = errors.New("publicationcommand: команда отклонена владельцем")

	// ErrNotFound — публикация у владельца отсутствует либо принадлежит другому
	// тенанту. Существование чужой записи не раскрывается.
	ErrNotFound = errors.New("publicationcommand: публикация не найдена")

	// ErrUnavailable — владелец контракта недоступен: контракт не подключён или
	// сбой транспорта.
	ErrUnavailable = errors.New("publicationcommand: владелец контракта недоступен")
)

// Material — материал публикации под конкретную площадку. Заголовок
// необязателен, текст обязателен.
type Material struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

// Target — один материал команды: площадка, учётная запись платформы и ключ
// идемпотентности, делающий повтор команды безопасным.
type Target struct {
	// PublicationID — идентификатор создаваемой публикации; назначает
	// потребитель, чтобы повтор команды был идемпотентен по идентификатору.
	PublicationID string `json:"publication_id"`
	// Platform — площадка назначения (значение VO publication-target).
	Platform string `json:"platform"`
	// Account — учётная запись платформы на площадке.
	Account string `json:"account"`
	// IdempotencyKey — ключ идемпотентности публикации.
	IdempotencyKey string `json:"idempotency_key"`
	// Material — подготовленный под площадку материал.
	Material Material `json:"material"`
}

// Command — команда на публикацию: источник-выжимка и материалы по площадкам.
type Command struct {
	// TenantID — тенант-владелец записи публикации.
	TenantID string `json:"tenant_id"`
	// Actor — оператор, инициировавший публикацию.
	Actor string `json:"actor"`
	// DigestID — идентификатор выжимки-источника.
	DigestID string `json:"digest_id"`
	// CommunityID — сообщество, по которому построена выжимка.
	CommunityID string `json:"community_id"`
	// Period — период сообщений выжимки.
	Period string `json:"period"`
	// CorrelationID — сквозной сценарий; пусто — владелец берёт идентификатор
	// первой публикации.
	CorrelationID string `json:"correlation_id,omitempty"`
	// CausationID — причина команды.
	CausationID string `json:"causation_id,omitempty"`
	// Targets — материалы команды, по одному на площадку.
	Targets []Target `json:"targets"`
}

// Validate проверяет обязательные поля команды. Ошибка называет все
// отсутствующие или некорректные поля, чтобы отказ был разбираем.
func (c Command) Validate() error {
	var invalid []string
	if strings.TrimSpace(c.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(c.Actor) == "" {
		invalid = append(invalid, "actor")
	}
	if strings.TrimSpace(c.DigestID) == "" {
		invalid = append(invalid, "digest_id")
	}
	if strings.TrimSpace(c.CommunityID) == "" {
		invalid = append(invalid, "community_id")
	}
	if strings.TrimSpace(c.Period) == "" {
		invalid = append(invalid, "period")
	}
	if len(c.Targets) == 0 {
		invalid = append(invalid, "targets")
	}
	for i, target := range c.Targets {
		if err := target.validate(); err != nil {
			invalid = append(invalid, fmt.Sprintf("targets[%d].%s", i, err.Error()))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("publicationcommand: publication-command: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// validate проверяет обязательные поля одного материала.
func (t Target) validate() (err error) {
	if strings.TrimSpace(t.PublicationID) == "" {
		return errors.New("publication_id")
	}
	if _, ok := allowedPlatforms[strings.ToLower(strings.TrimSpace(t.Platform))]; !ok {
		return fmt.Errorf("platform (%q)", t.Platform)
	}
	if strings.TrimSpace(t.Account) == "" {
		return errors.New("account")
	}
	if strings.TrimSpace(t.IdempotencyKey) == "" {
		return errors.New("idempotency_key")
	}
	if strings.TrimSpace(t.Material.Body) == "" {
		return errors.New("material.body")
	}
	return nil
}

// Accepted — принятая владельцем публикация.
type Accepted struct {
	// PublicationID — идентификатор публикации.
	PublicationID string `json:"publication_id"`
	// Platform — площадка назначения.
	Platform string `json:"platform"`
	// Status — статус публикации после приёма команды.
	Status string `json:"status"`
	// Shortened — был ли материал сокращён под предел площадки.
	Shortened bool `json:"shortened,omitempty"`
}

// Result — результат приёма команды: принятые публикации.
type Result struct {
	Publications []Accepted `json:"publications"`
}

// Port — команда владельцу публикаций. Реализация принимает материалы и отвечает
// их состоянием. ErrRejected и ErrNotFound — доменные исходы, ErrUnavailable —
// сбой транспорта или отсутствие контракта.
type Port interface {
	Submit(ctx context.Context, cmd Command) (Result, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// SubmitFunc реализует приём команды.
	SubmitFunc func(ctx context.Context, cmd Command) (Result, error)
}

// Submit реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает результат.
func (h InProcHandler) Submit(ctx context.Context, cmd Command) (Result, error) {
	if h.SubmitFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.SubmitFunc(ctx, cmd)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
