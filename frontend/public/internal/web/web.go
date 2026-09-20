// Package web — HTTP-адаптер публичных страниц публичного edge.
//
// Слой решает только то, что относится к вебу: маршрут, разбор запроса,
// проверку формы, код ответа, заголовки безопасности и рендеринг разметки.
// Доменных правил здесь нет: каждое содержательное решение (что за страница,
// куда ведёт ссылка, что происходит с заявкой) принимает владелец данных, а
// edge обращается к нему через входящие порты (internal/ports).
//
// Экран = полная страница. Модель «полная страница или фрагмент» консоли
// оператора (web-frontend-architecture, Решение 4) здесь вырождается: посетитель
// приходит из письма или поиска, ему нужна целая страница, поэтому HTMX-
// фрагментов у публичного контура нет.
//
// Ограничения, которые слой обязан удерживать:
//
//   - нет аутентификации и сессии оператора — посетитель публичной страницы
//     анонимен, и тенанта у него нет;
//   - нет доступа к хранилищам: пакет не импортирует ни одного драйвера БД
//     (tools/importlint, правило для lead-gen/frontend/public/...);
//   - персональные данные не журналируются и не кэшируются: в журнал идут
//     маршрут, код и длительность, а страницы формы несут Cache-Control:
//     no-store.
package web

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"lead-gen/frontend/components"
	"lead-gen/frontend/public/internal/ports"
)

// Config — зависимости HTTP-слоя. Все источники и приёмники обязательны:
// edge без владельца данных не работает, и молчаливая заглушка вместо
// отсутствующей зависимости скрыла бы ошибку сборки.
type Config struct {
	// DefaultLanding — ссылка на лендинг, отдаваемый по корню домена. Пустая
	// строка означает, что корень отвечает «страница не найдена»: edge
	// обслуживает много страниц и не выбирает главную за владельца.
	DefaultLanding ports.PageRef

	// Landings — источник наполнения лендингов.
	Landings ports.LandingSource
	// Presentations — источник наполнения презентаций.
	Presentations ports.PresentationSource
	// Submissions — приёмник заявок посетителей.
	Submissions ports.SubmissionSink
	// Tracking — разрешение трекинг-переходов и учёт состоявшихся переходов.
	Tracking ports.TrackingResolver

	// Logger — журнал процесса; nil означает slog.Default().
	Logger *slog.Logger
	// Now — источник времени; nil означает time.Now().UTC(). Подменяется в
	// тестах, чтобы проверить проставляемый момент приёма заявки и перехода.
	Now func() time.Time
}

// handler держит разобранные шаблоны и зависимости слоя.
type handler struct {
	config Config
	pages  *components.Set
	log    *slog.Logger
	now    func() time.Time
}

// New собирает HTTP-адаптер публичных страниц. Ошибка возвращается, если не
// задана зависимость или не разобрались шаблоны: конфигурация проверяется при
// сборке процесса, а не при первом запросе посетителя.
func New(config Config) (http.Handler, error) {
	if config.Landings == nil {
		return nil, errors.New("web: не задан источник наполнения лендингов")
	}
	if config.Presentations == nil {
		return nil, errors.New("web: не задан источник наполнения презентаций")
	}
	if config.Submissions == nil {
		return nil, errors.New("web: не задан приёмник заявок")
	}
	if config.Tracking == nil {
		return nil, errors.New("web: не задан разрешитель трекинг-переходов")
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	pages, err := parsePages()
	if err != nil {
		return nil, err
	}

	handler := &handler{config: config, pages: pages, log: logger, now: now}
	return handler.middleware(handler.routes()), nil
}

// routes собирает маршруты публичных страниц.
//
// Ссылки страниц и форм лежат в пути, а не в параметре запроса: адрес страницы
// виден посетителю и попадает в трекинг-письмо, поэтому он обязан быть
// стабильным и не зависеть от скрытых полей.
func (h *handler) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.handleRootLanding)
	mux.HandleFunc("GET /l/{ref}", h.handleLanding)
	mux.HandleFunc("GET /p/{ref}", h.handlePresentation)
	mux.HandleFunc("GET /f/{ref}", h.handleForm)
	mux.HandleFunc("POST /f/{ref}", h.handleFormSubmit)
	mux.HandleFunc("GET /t/{ref}", h.handleTracking)
	mux.Handle("GET /static/", http.StripPrefix("/static/", h.staticFiles()))
	mux.HandleFunc("GET /healthz", handleHealth)
	return mux
}

// middleware навешивает журнал, восстановление после паники и заголовки
// безопасности. Порядок важен: журнал снаружи видит код ответа и после
// восстановления, а заголовки выставляются до записи тела. Заголовки — общий
// компонент (frontend/components); политика CSP принадлежит edge.
func (h *handler) middleware(next http.Handler) http.Handler {
	secured := components.SecurityHeaders(contentSecurityPolicy)(next)
	return h.logRequests(h.recoverPanic(secured))
}

// staticFiles раздаёт встроенные статические файлы edge. Отдельный каталог, а
// не раздача исходников: наружу уходит только оформление.
func (h *handler) staticFiles() http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		// fs.Sub не падает на корректном embed: путь каталога проверен сборкой.
		panic(fmt.Sprintf("web: каталог статики недоступен: %v", err))
	}
	return http.FileServerFS(static)
}

// handleHealth — проба живости процесса. Контейнер без оболочки не умеет
// ходить по HTTP сам, поэтому пробу снимает внешний наблюдатель.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}
