package contentversion

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "content-version"

// SchemaVersion — текущая версия схемы контракта. Версионирование — semver:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл без
// изменения состава полей.
//
// 1.1.0 — MINOR: добавлено опциональное поле Version.Subject (тема материала
// письма). Изменение аддитивно, потребители версии 1.0.0 продолжают работать.
const SchemaVersion = "1.1.0"

// ErrNotFound — версии контента с запрошенной ссылкой у владельца нет либо пакет
// принадлежит другому тенанту. Существование чужого пакета этим отказом не
// раскрывается: «нет» и «не твоя» неотличимы.
var ErrNotFound = errors.New("contentversion: версия контента не найдена")

// ErrUnavailable — владелец контента недоступен: контракт не подключён или сбой
// транспорта.
var ErrUnavailable = errors.New("contentversion: владелец контента недоступен")

// Query — запрос неизменяемой версии контента. VersionID пуст — владелец отдаёт
// последнюю версию пакета. TenantID обязателен: чтение версии чужого тенанта
// завершается ErrNotFound.
type Query struct {
	AssetID   string
	VersionID string
	TenantID  string
}

// Version — неизменяемая версия контента. Опубликованная версия не редактируется:
// новый текст создаёт новую версию, прежняя остаётся как есть.
type Version struct {
	AssetID   string
	VersionID string
	Revision  int
	Language  string
	Region    string

	// Subject — тема материала письма: заголовок версии/материала, из которого
	// собирается письмо. Поле опциональное: пусто означает, что у материала нет
	// заголовка, и тема честно не выдумывается (AGENTS.md §3).
	Subject string

	Body string
}

// Source — чтение версии контента у владельца. ErrNotFound — доменный исход,
// ErrUnavailable — сбой транспорта или отсутствие контракта.
type Source interface {
	Version(ctx context.Context, q Query) (Version, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// VersionFunc реализует чтение версии контента.
	VersionFunc func(ctx context.Context, q Query) (Version, error)
}

// Version реализует Source. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает версию.
func (h InProcHandler) Version(ctx context.Context, q Query) (Version, error) {
	if h.VersionFunc == nil {
		return Version{}, ErrUnavailable
	}
	return h.VersionFunc(ctx, q)
}

// InProc заворачивает обработчики в Source для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Source { return handler }
