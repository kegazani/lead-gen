package components

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"
)

// dateFormat — макет даты извлечения в разметке: год-месяц-день. Формат
// однозначен независимо от языка интерфейса и не требует каталога сообщений.
const dateFormat = "2006-01-02"

// Tab — вкладка навигации: ключ (машинное имя для сопоставления с активной
// вкладкой), подпись и адрес перехода. Компонент не знает, куда ведут вкладки
// и что за ними: это данные представления.
type Tab struct {
	Key   string
	Label string
	Href  string
}

// Tabs — панель вкладок: список вкладок и ключ активной. Компонент размечает
// ссылки и подсвечивает активную; смысл вкладок и маршрутизацию определяет
// владелец экрана.
type Tabs struct {
	Items  []Tab
	Active string
}

// Provenance — происхождение поля: URL источника и дата извлечения. Компонент
// показывает источник ссылкой и дату рядом с ним; что за поле и откуда
// значение — забота владельца экрана.
type Provenance struct {
	Source string
	Date   time.Time
}

// RenderTabs размечает панель вкладок в w. Активная вкладка определяется по
// ключу Tabs.Active и получает класс is-active и aria-current.
func RenderTabs(w io.Writer, tabs Tabs) error {
	return renderFragment(w, "tabs", tabs)
}

// RenderStatusBadge размечает бейдж статуса в w. Класс выводится из строки
// статуса предсказуемо (status-<статус>), поэтому статусы, добавленные позже,
// не требуют правки разметки; метка показывается как есть.
func RenderStatusBadge(w io.Writer, status string) error {
	return renderFragment(w, "status-badge", statusBadge{
		Label: status,
		Class: StatusClass(status),
	})
}

// RenderProvenance размечает происхождение поля в w: источник ссылкой и дату
// извлечения рядом с ним.
func RenderProvenance(w io.Writer, provenance Provenance) error {
	return renderFragment(w, "provenance", provenanceView{
		Source: provenance.Source,
		Date:   provenance.Date.Format(dateFormat),
	})
}

// RenderNav размечает вертикальную навигацию (sidebar) в w. Активный пункт
// определяется по флагу NavItem.Active.
func RenderNav(w io.Writer, nav Nav) error {
	return renderFragment(w, "nav", nav)
}

// statusBadge — модель разметки бейджа статуса: метка, уже нормализованный
// CSS-класс и необязательная подсказка при наведении. Шаблон не содержит логики —
// только показывает готовые строки.
type statusBadge struct {
	Label string
	Class string
	Title string
}

// provenanceView — модель разметки происхождения: источник и уже
// отформатированная дата. Шаблон не форматирует время — он показывает готовые
// строки, поэтому смена формата даты не трогает разметку.
type provenanceView struct {
	Source string
	Date   string
}

// StatusClass нормализует статус в безопасный CSS-класс: строчные буквы и
// цифры остаются, остальные символы заменяются дефисом. Пустой статус даёт
// пустой класс — бейдж остаётся без модификатора.
//
// Экспортирован для потребителей, которые размечают бейдж в шаблоне полной
// страницы ({{ template "status-badge" ... }}) и заранее готовят класс.
func StatusClass(status string) string {
	var builder strings.Builder
	builder.Grow(len(status))
	for _, r := range strings.ToLower(status) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

// renderFragment разбирает один встроенный шаблон-фрагмент и исполняет его в w.
// Фрагменты маленькие и неизменные, поэтому разбор на каждый вызов проще
// общего реестра с состоянием; ошибка возвращается сразу, как и в Set.Render.
func renderFragment(w io.Writer, name string, data any) error {
	parsed, err := template.New(name).Funcs(templateFuncs(DefaultMessages())).ParseFS(baseFS, "templates/"+name+".html")
	if err != nil {
		return fmt.Errorf("components: разбор фрагмента %s: %w", name, err)
	}
	return parsed.ExecuteTemplate(w, name, data)
}
