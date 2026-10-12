package components

import (
	"bytes"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// BasePath — внешний префикс пути сервера консоли под единым origin (обратный
// прокси admin-proxy). Например, "/leads" для консоли lead-management. Пустое
// значение означает, что сервер смонтирован в корень origin и переписывание
// адресов не требуется.
type BasePath string

// Path возвращает адрес с префиксом: BasePath("/leads").Path("/jobs") == "/leads/jobs".
// Корень даёт префикс с завершающим слэшем ("/leads/"), чтобы относительные ссылки
// браузера резолвились внутри префикса.
func (b BasePath) Path(p string) string {
	return JoinPrefix(string(b), p)
}

// JoinPrefix склеивает внешний префикс и путь сервера в один адрес, нормализуя
// разделители. Пустой префикс возвращает путь без изменений. Это единая точка
// сборки адресов консоли под префиксом: навигация, редиректы и ссылки шаблонов.
func JoinPrefix(base, path string) string {
	base = normalizeBasePath(base)
	if base == "" {
		if path == "" {
			return "/"
		}
		return path
	}
	if path == "" || path == "/" {
		return base + "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// normalizeBasePath приводит внешний префикс к канонической форме без ведущего
// пустого значения: "/leads" (без завершающего слэша). Пустая строка и "/" дают
// пустой префикс (корень origin).
func normalizeBasePath(base string) string {
	base = strings.TrimSpace(base)
	base = strings.TrimSuffix(base, "/")
	if base == "" || base == "/" {
		return ""
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	return base
}

// htmlURLAttrRe находит значения локальных URL в атрибутах разметки, которые
// серверный шаблон и HTMX используют для навигации и форм. Переписываются только
// значения с двойными кавычками (html/template всегда выводит их так). Список
// атрибутов покрывает ссылки, статику, формы и гипермедиа-атрибуты HTMX.
var htmlURLAttrRe = regexp.MustCompile(`(?i)(\b(?:href|src|action|formaction|hx-get|hx-post|hx-put|hx-delete|hx-patch|hx-push-url|hx-replace-url|data-url|data-sse-url)\s*=\s*")([^"]*)(")`)

// htmlHeadOpenRe находит открывающий тег <head>, чтобы вставить в него мета-тег с
// внешним префиксом: клиентские скрипты (SSE-подписка, перенос лида) читают его и
// строят адреса API без дублирования префикса в JS-файлах.
var htmlHeadOpenRe = regexp.MustCompile(`(?i)(<head[^>]*>)`)

// PrefixMiddleware монтирует сервер консоли под внешним префиксом пути: снимает
// префикс из входящего пути (маршруты остаются корневыми), добавляет его обратно
// в заголовок Location редиректов и переписывает локальные URL в HTML-ответах.
// externalPrefixes — внешние префиксы ДРУГИХ сервисов контура: такие пути уже
// являются адресами внутри единого origin (навигация между консолями) и НЕ
// переписываются. Ответы, не являющиеся HTML (SSE, JSON, CSV, статика), и
// скачиваемые документы (Content-Disposition: attachment) не трогаются. Пустой
// префикс возвращает следующий обработчик без изменений.
func PrefixMiddleware(base string, next http.Handler, externalPrefixes ...string) http.Handler {
	prefix := normalizeBasePath(base)
	if prefix == "" {
		return next
	}
	externals := normalizePrefixes(externalPrefixes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Снять префикс: mux сервиса объявляет маршруты от корня (/jobs, /validation).
		// Запрос не под префиксом (например, клиентский портал того же сервиса,
		// смонтированный без префикса) передаётся без изменений — его ответы не
		// переписываются.
		matched := false
		if r.URL.Path == prefix {
			r = cloneWithPath(r, "/")
			matched = true
		} else if strings.HasPrefix(r.URL.Path, prefix+"/") {
			r = cloneWithPath(r, strings.TrimPrefix(r.URL.Path, prefix))
			matched = true
		}
		if !matched {
			next.ServeHTTP(w, r)
			return
		}
		rw := &prefixResponseWriter{ResponseWriter: w, prefix: prefix, externals: externals}
		defer rw.flush()
		next.ServeHTTP(rw, r)
	})
}

// normalizePrefixes приводит список внешних префиксов к канонической форме и
// отбрасывает пустые значения.
func normalizePrefixes(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		if n := normalizeBasePath(p); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// cloneWithPath возвращает копию запроса с изменённым путём: исходный запрос не
// мутируется, RawPath сбрасывается, чтобы не разойтись с новым Path.
func cloneWithPath(r *http.Request, path string) *http.Request {
	cloned := r.Clone(r.Context())
	if path == "" {
		path = "/"
	}
	cloned.URL.Path = path
	cloned.URL.RawPath = ""
	return cloned
}

// prefixResponseWriter буферизует HTML-ответ, чтобы переписать в нём локальные
// URL под внешний префикс, и переписывает заголовок Location. Не-HTML ответы
// (SSE, JSON, статика) пишутся сразу без буферизации.
type prefixResponseWriter struct {
	http.ResponseWriter
	prefix    string
	externals []string

	status      int
	wroteHeader bool
	rewrite     bool
	buf         bytes.Buffer
}

// WriteHeader перехватывает запись заголовков: переписывает Location и решает,
// нужно ли буферизовать тело (HTML без Content-Disposition: attachment).
func (w *prefixResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	if loc := w.Header().Get("Location"); loc != "" {
		w.Header().Set("Location", prefixLocalURL(w.prefix, loc, w.externals))
	}
	ct := w.Header().Get("Content-Type")
	if strings.HasPrefix(ct, "text/html") && !isAttachment(w.Header()) {
		w.rewrite = true
		w.Header().Del("Content-Length")
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write накапливает буфер при переписывании, иначе пишет сразу.
func (w *prefixResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.rewrite {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// Flush поддерживает потоковые ответы (SSE): буферизуемый HTML сбрасывать нечего,
// остальные сбрасываются сразу. http.NewResponseController находит Flush через
// Unwrap и не роняет SSE-обработчик.
func (w *prefixResponseWriter) Flush() {
	if w.rewrite {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap раскрывает базовый ResponseWriter для http.ResponseController и
// опциональных интерфейсов (Flusher, Hijacker).
func (w *prefixResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// flush завершает буферизованный ответ: переписывает URL, вставляет мета-тег с
// префиксом и пишет тело с корректным Content-Length. Не-HTML ответы не трогаются.
func (w *prefixResponseWriter) flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.rewrite {
		return
	}
	body := rewriteHTML(w.prefix, w.buf.String(), w.externals)
	body = injectBasePathMeta(w.prefix, body)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write([]byte(body))
}

// rewriteHTML переписывает локальные URL в атрибутах разметки, добавляя префикс.
// Внешние URL (http/https), scheme-relative (//), якоря и пути под префиксами
// других сервисов не трогаются.
func rewriteHTML(prefix, body string, externals []string) string {
	return htmlURLAttrRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := htmlURLAttrRe.FindStringSubmatch(match)
		if len(sub) != 4 {
			return match
		}
		return sub[1] + prefixLocalURL(prefix, sub[2], externals) + sub[3]
	})
}

// injectBasePathMeta вставляет мета-тег с внешним префиксом в <head>, чтобы
// клиентские скрипты строили адреса API (SSE, fetch) без дублирования префикса.
func injectBasePathMeta(prefix, body string) string {
	if prefix == "" || !htmlHeadOpenRe.MatchString(body) {
		return body
	}
	return htmlHeadOpenRe.ReplaceAllString(body, `${1}<meta name="admin-base-path" content="`+html.EscapeString(prefix)+`">`)
}

// prefixLocalURL добавляет префикс к локальному абсолютному пути. Внешние,
// относительные и scheme-relative адреса возвращаются без изменений, как и пути,
// уже лежащие под одним из внешних префиксов других сервисов.
func prefixLocalURL(prefix, u string, externals []string) string {
	u = strings.TrimSpace(u)
	if u == "" || !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return u
	}
	for _, ext := range externals {
		if u == ext || strings.HasPrefix(u, ext+"/") {
			return u
		}
	}
	return prefix + u
}

// isAttachment сообщает, помечен ли ответ как скачиваемый документ
// (Content-Disposition: attachment) — такие ответы не переписываются.
func isAttachment(header http.Header) bool {
	return strings.HasPrefix(strings.ToLower(header.Get("Content-Disposition")), "attachment")
}
