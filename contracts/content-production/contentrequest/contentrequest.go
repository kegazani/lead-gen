package contentrequest

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "content-request"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл без
// изменения состава полей.
const SchemaVersion = "1.1.0"

// ErrRejected — запрос отклонён владельцем по доменной причине (пустой текст,
// неизвестный формат, недопустимая локаль). Потребитель различает этот отказ от
// сбоя транспорта.
var ErrRejected = errors.New("contentrequest: запрос производства контента отклонён владельцем")

// ErrUnavailable — владелец контента недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех.
var ErrUnavailable = errors.New("contentrequest: владелец контента недоступен")

// Intent — вход команды производства контента. RequestID назначает потребитель,
// чтобы повтор команды был идемпотентен по идентификатору.
type Intent struct {
	// RequestID — идентификатор запроса и ключ идемпотентности.
	RequestID string
	// TenantID — тенант-владелец контента.
	TenantID string
	// AssetID — контент-пакет, который производится.
	AssetID string
	// Format — целевой формат артефакта: landing-page, presentation или document.
	Format string
	// Language — язык контента (обязателен).
	Language string
	// Region — регион контента (необязателен).
	Region string
	// TemplateID — шаблон рендера.
	TemplateID string
	// Body — исходный текст. Для нового пакета обязателен; для существующего
	// непустой и отличный текст публикует новую версию.
	Body string
	// Mode задаёт способ обработки. Пустое значение сохраняет legacy render;
	// version-only фиксирует точные Subject и Body без задания рендеринга.
	Mode string `json:"mode,omitempty"`
	// Subject — точная тема версии в режиме version-only.
	Subject *string `json:"subject,omitempty"`
	// Actor — инициатор производства.
	Actor string
	// CorrelationID — сквозной сценарий; пусто — владелец берёт RequestID.
	CorrelationID string
}

// Result — результат команды: контент-пакет, версия, задание рендеринга и его
// состояние.
type Result struct {
	RequestID   string
	AssetID     string
	VersionID   string
	RenderJobID string
	State       string
}

const (
	ModeVersionOnly = "version-only"
)

// Port — команда производства контента владельцу. ErrRejected — доменный отказ,
// ErrUnavailable — сбой транспорта или отсутствие контракта.
type Port interface {
	RequestContent(ctx context.Context, in Intent) (Result, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// RequestFunc реализует команду производства контента.
	RequestFunc func(ctx context.Context, in Intent) (Result, error)
}

// RequestContent реализует Port. Незаданная функция честно сообщает о
// недоступности владельца, а не выдумывает результат.
func (h InProcHandler) RequestContent(ctx context.Context, in Intent) (Result, error) {
	if h.RequestFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.RequestFunc(ctx, in)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
