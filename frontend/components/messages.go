package components

// Локализация общего слоя: пользовательские тексты компонентов живут в каталоге
// сообщений, а не в шаблонах. Шаблон ссылается на ключ через функцию `text`, и
// поиск по каталогу шаблонов не находит строковых литералов пользовательского
// текста (web-frontend-architecture, срез 7, 7.1; design.md, Решение 12).
//
// Каталог сознательно простой: ключ → строка на локаль. Сейчас заполнена одна
// локаль (ru) — язык консоли; добавление локали это новый набор значений, а не
// правка шаблонов. Форматы дат, чисел, валют и часовых поясов локализуются
// отдельно, в Formatter (см. format.go).

// Locale — язык интерфейса.
type Locale string

// LocaleRu — русский язык интерфейса: язык разметки по умолчанию (LangRu).
const LocaleRu Locale = "ru"

// MessageKey — ключ сообщения каталога. Ключи машинные (ASCII), поэтому в
// шаблонах нет пользовательских строк — только ссылки на каталог.
type MessageKey string

// Ключи навигации и каркаса.
const (
	msgNavLabel       MessageKey = "nav.label"
	msgNavThemeLabel  MessageKey = "nav.theme.label"
	msgNavThemeToggle MessageKey = "nav.theme.toggle"
	msgNavThemeAria   MessageKey = "nav.theme.aria"
	msgNavThemeSystem MessageKey = "nav.theme.system"
	msgNavThemeLight  MessageKey = "nav.theme.light"
	msgNavThemeDark   MessageKey = "nav.theme.dark"
	msgSkipToContent  MessageKey = "layout.skip_to_content"
)

// Ключи подписей разделов общей навигации консоли.
const (
	msgSectionRequests      MessageKey = "console.section.requests"
	msgSectionExecution     MessageKey = "console.section.execution"
	msgSectionLeads         MessageKey = "console.section.leads"
	msgSectionReports       MessageKey = "console.section.reports"
	msgSectionOutreach      MessageKey = "console.section.outreach"
	msgSectionTenant        MessageKey = "console.section.tenant"
	msgSectionUsers         MessageKey = "console.section.users"
	msgSectionRegistrations MessageKey = "console.section.registrations"
	msgSectionIntegrations  MessageKey = "console.section.integrations"
	msgSectionAudit         MessageKey = "console.section.audit"
	msgSectionTopology      MessageKey = "console.section.topology"
	msgSectionOperations    MessageKey = "console.section.operations"
)

// Ключи области живых объявлений (aria-live).
const (
	msgLiveLabel MessageKey = "live.label"
)

// Ключи словаря верификации адресов (BRD §3.18): подписи и значки статусов.
const (
	msgVerifyDeliverable     MessageKey = "verify.deliverable"
	msgVerifyRisky           MessageKey = "verify.risky"
	msgVerifyUnknown         MessageKey = "verify.unknown"
	msgVerifyInvalid         MessageKey = "verify.invalid"
	msgVerifyIconDeliverable MessageKey = "verify.icon.deliverable"
	msgVerifyIconRisky       MessageKey = "verify.icon.risky"
	msgVerifyIconUnknown     MessageKey = "verify.icon.unknown"
	msgVerifyIconInvalid     MessageKey = "verify.icon.invalid"
	// msgVerifyCheckedAt — подпись «проверено»; дата подставляется Formatter.
	msgVerifyCheckedAt MessageKey = "verify.checked_at"
)

// ruCatalog — каталог сообщений для LocaleRu. Единственный источник
// пользовательского текста общего слоя.
var ruCatalog = map[MessageKey]string{
	msgNavLabel:       "Основная навигация",
	msgNavThemeLabel:  "Тема",
	msgNavThemeToggle: "Тема оформления",
	// %s заменяется названием текущего режима оформления (см. theme.js).
	msgNavThemeAria:   "Тема оформления: %s. Нажмите, чтобы сменить.",
	msgNavThemeSystem: "Как в системе",
	msgNavThemeLight:  "Светлая",
	msgNavThemeDark:   "Тёмная",
	msgSkipToContent:  "Перейти к содержимому",

	msgSectionRequests:  "Заявки",
	msgSectionExecution: "Исполнение",
	msgSectionLeads:     "Лиды",
	msgSectionReports:   "Отчёты",
	msgSectionOutreach:  "Рассылки",

	msgSectionTenant:        "Тенант",
	msgSectionUsers:         "Пользователи",
	msgSectionRegistrations: "Регистрации",
	msgSectionIntegrations:  "Интеграции",
	msgSectionAudit:         "Аудит",
	msgSectionTopology:      "Топология",
	msgSectionOperations:    "Операции",

	msgLiveLabel: "Объявления",

	msgVerifyDeliverable: "доставляемый",
	msgVerifyRisky:       "рискованный",
	msgVerifyUnknown:     "неизвестно",
	msgVerifyInvalid:     "недействительный",

	msgVerifyIconDeliverable: "✓",
	msgVerifyIconRisky:       "!",
	msgVerifyIconUnknown:     "?",
	msgVerifyIconInvalid:     "✕",

	msgVerifyCheckedAt: "Проверено",
}

// Messages — каталог сообщений конкретной локали. nil-значение безопасно: методы
// откатываются к локали по умолчанию, поэтому потребителю не нужно проверять
// указатель.
type Messages struct {
	locale Locale
	values map[MessageKey]string
}

// defaultMessages — каталог по умолчанию. Инициализируется прямым присваиванием
// (не через функцию), чтобы порядок инициализации пакета гарантировал готовность
// ruCatalog: зависимость видна анализатору порядка.
var defaultMessages = &Messages{locale: LocaleRu, values: ruCatalog}

// NewMessages возвращает каталог для локали. Неизвестная локаль не выдумывается:
// возвращается каталог по умолчанию (ru).
func NewMessages(locale Locale) *Messages {
	if locale == LocaleRu {
		return &Messages{locale: LocaleRu, values: ruCatalog}
	}
	return defaultMessages
}

// WithMessages возвращает каталог, дополняющий базовый сообщениями потребителя.
// Нужен экранам, которые добавляют свои тексты, не расходясь с общим каталогом:
// базовые ключи остаются, совпадающие ключи потребителя перекрывают их.
func WithMessages(base *Messages, extra map[MessageKey]string) *Messages {
	if base == nil {
		base = DefaultMessages()
	}
	values := make(map[MessageKey]string, len(ruCatalog)+len(extra))
	for key, value := range ruCatalog {
		values[key] = value
	}
	for key, value := range base.values {
		values[key] = value
	}
	for key, value := range extra {
		values[key] = value
	}
	return &Messages{locale: base.Locale(), values: values}
}

// DefaultMessages — каталог сообщений по умолчанию (ru).
func DefaultMessages() *Messages { return defaultMessages }

// Locale возвращает локаль каталога.
func (m *Messages) Locale() Locale {
	if m == nil || m.locale == "" {
		return LocaleRu
	}
	return m.locale
}

// Text возвращает сообщение по ключу. Неизвестный ключ не маскируется пустой
// строкой: возвращается сам ключ, и это видно в разметке и тестах.
func (m *Messages) Text(key MessageKey) string {
	if m != nil {
		if value, ok := m.values[key]; ok {
			return value
		}
	}
	if value, ok := ruCatalog[key]; ok {
		return value
	}
	return string(key)
}

// Label возвращает читаемую подпись машинного кода (статуса, фазы, состояния):
// ключ каталога собирается как prefix + "." + нормализованный код (та же
// нормализация, что у StatusClass: нижний регистр, разделители — дефис), поэтому
// "RUNNING", "awaiting-approval" и "not warmed" находят ключи "….running",
// "….awaiting-approval" и "….not-warmed". Неизвестный код не маскируется и не
// превращается в ключ: возвращается сам код, чтобы новое состояние было видно, а
// не пропадало.
func (m *Messages) Label(prefix, code string) string {
	normalized := StatusClass(code)
	if normalized == "" {
		return code
	}
	key := MessageKey(prefix + "." + normalized)
	if m.Has(key) {
		return m.Text(key)
	}
	return code
}

// Has сообщает, объявлен ли ключ в каталоге.
func (m *Messages) Has(key MessageKey) bool {
	if m != nil {
		if _, ok := m.values[key]; ok {
			return true
		}
	}
	_, ok := ruCatalog[key]
	return ok
}
