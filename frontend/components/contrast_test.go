package components

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// contrastTarget — порог WCAG 2.1 AA для обычного текста (web-frontend-architecture,
// срез 7, 7.4; design.md, Решение 13).
const contrastTarget = 4.5

// color — цвет в линейном пространстве не задаём: храним sRGB-компоненты [0,1] и
// прозрачность.
type color struct {
	r, g, b, a float64
}

// TestStatusContrastInBothThemes проверяет критерий задачи 7.4: подписи статусов и
// верификации читаются на своей подложке в светлой и тёмной темах с контрастом не
// ниже AA.
func TestStatusContrastInBothThemes(t *testing.T) {
	css := readCSS(t)
	themes := []struct {
		name   string
		tokens map[string]string
	}{
		{"light", parseThemeTokens(t, css, false)},
		{"dark", parseThemeTokens(t, css, true)},
	}

	// Пары «цвет подписи — мягкая подложка бейджа».
	pairs := [][2]string{
		{"--status-ink-ok", "--ok-soft"},
		{"--status-ink-warn", "--warn-soft"},
		{"--status-ink-bad", "--fail-soft"},
		{"--status-ink-neutral", "--neutral-soft"},
		{"--status-ink-notwarmed", "--stage-notwarmed-soft"},
		{"--status-ink-email", "--stage-email-soft"},
		{"--status-ink-cold", "--stage-cold-soft"},
	}

	for _, theme := range themes {
		surface := mustColor(t, theme.name, theme.tokens, "--surface")
		background := composite(mustColor(t, theme.name, theme.tokens, "--paper-2"), color{a: 1})
		for _, pair := range pairs {
			ink := mustColor(t, theme.name, theme.tokens, pair[0])
			soft := mustColor(t, theme.name, theme.tokens, pair[1])
			ratio := contrastRatio(ink, composite(soft, surface))
			t.Logf("%s %s на %s: %.2f:1", theme.name, pair[0], pair[1], ratio)
			if ratio < contrastTarget {
				t.Errorf("%s: %s на %s = %.2f:1, ниже AA %.1f", theme.name, pair[0], pair[1], ratio, contrastTarget)
			}
		}
		// Основной и второстепенный текст на поверхностях страницы.
		for _, check := range []struct {
			ink, bg string
		}{
			{"--ink", "--surface"},
			{"--ink-soft", "--surface"},
			{"--ink-soft", "--paper-2"},
		} {
			ink := mustColor(t, theme.name, theme.tokens, check.ink)
			bg := composite(mustColor(t, theme.name, theme.tokens, check.bg), background)
			ratio := contrastRatio(ink, bg)
			t.Logf("%s %s на %s: %.2f:1", theme.name, check.ink, check.bg, ratio)
			if ratio < contrastTarget {
				t.Errorf("%s: %s на %s = %.2f:1, ниже AA %.1f", theme.name, check.ink, check.bg, ratio, contrastTarget)
			}
		}
	}
}

// TestStatusMarkersEncodeTypeWithoutColor проверяет критерий задачи 7.4: смысл
// статуса несут подпись и форма маркера; форма различается по типу исхода, а не
// только по цвету.
func TestStatusMarkersEncodeTypeWithoutColor(t *testing.T) {
	css := readCSS(t)
	checks := []struct {
		why     string
		pattern string
	}{
		{"маркер статуса задан и не пуст", `\.status::before\s*\{[^}]*content:\s*""`},
		{"ожидание — полый маркер", `\.status-queued::before,[^{]*\{[^}]*background:\s*transparent`},
		{"сбой — ромб (повёрнутый квадрат)", `\.status-failed::before,[^{]*\{[^}]*transform:\s*rotate\(45deg\)`},
		{"результат процесса получил отдельный класс", `\.status-active\s*\{`},
	}
	for _, check := range checks {
		if !regexp.MustCompile(`(?s)` + check.pattern).MatchString(css) {
			t.Errorf("оформление не подтверждает «%s»", check.why)
		}
	}
}

// TestCockpitStatusContrastInBothThemes проверяет критерий задачи 6.6: подписи
// статусов экрана операций (категории цифр и состояния агента) читаются на своей
// подложке в светлой и тёмной темах с контрастом не ниже AA. Классы совпадают с
// теми, что выводит владелец экрана (categoryStateClass/agentStateClass).
func TestCockpitStatusContrastInBothThemes(t *testing.T) {
	css := readCSS(t)
	classes := []string{"active", "queued", "running", "done", "failed"}
	themes := []struct {
		name   string
		tokens map[string]string
	}{
		{"light", parseThemeTokens(t, css, false)},
		{"dark", parseThemeTokens(t, css, true)},
	}
	for _, theme := range themes {
		surface := mustColor(t, theme.name, theme.tokens, "--surface")
		for _, class := range classes {
			selector := ".status-" + class
			block := cssBlockFor(t, css, selector)
			ink := mustColorValue(t, theme.name, theme.tokens, declarationValue(t, selector, block, "color"))
			background := composite(mustColorValue(t, theme.name, theme.tokens, declarationValue(t, selector, block, "background")), surface)
			ratio := contrastRatio(ink, background)
			t.Logf("%s %s: %.2f:1", theme.name, selector, ratio)
			if ratio < contrastTarget {
				t.Errorf("%s: %s = %.2f:1, ниже AA %.1f", theme.name, selector, ratio, contrastTarget)
			}
		}
		// Подсветка выбранного среза графа (tr.is-active) не должна ронять
		// контраст основного текста.
		active := composite(mustColor(t, theme.name, theme.tokens, "--accent-soft"), surface)
		ratio := contrastRatio(mustColor(t, theme.name, theme.tokens, "--ink"), active)
		t.Logf("%s --ink на --accent-soft: %.2f:1", theme.name, ratio)
		if ratio < contrastTarget {
			t.Errorf("%s: --ink на --accent-soft = %.2f:1, ниже AA %.1f", theme.name, ratio, contrastTarget)
		}
	}
}

// readCSS читает общее оформление из встроенного каталога.
func readCSS(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(StaticFS(), "console.css")
	if err != nil {
		t.Fatalf("чтение console.css: %v", err)
	}
	return string(data)
}

// parseThemeTokens собирает токены темы: базовые :root плюс переопределения
// тёмной темы, если dark=true. Комментарии удаляются, значения var() остаются для
// разрешения в mustColor.
func parseThemeTokens(t *testing.T, css string, dark bool) map[string]string {
	t.Helper()
	withoutComments := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	base := regexp.MustCompile(`(?s):root\s*\{([^}]*)\}`)
	darkPattern := regexp.MustCompile(`(?s):root\[data-theme="dark"\]\s*\{([^}]*)\}`)

	tokens := map[string]string{}
	for _, match := range base.FindAllStringSubmatch(withoutComments, -1) {
		collectDeclarations(match[1], tokens)
	}
	if dark {
		for _, match := range darkPattern.FindAllStringSubmatch(withoutComments, -1) {
			collectDeclarations(match[1], tokens)
		}
	}
	return tokens
}

// collectDeclarations разбирает объявления `--name: value;` в карту.
func collectDeclarations(block string, tokens map[string]string) {
	for _, declaration := range strings.Split(block, ";") {
		name, value, ok := strings.Cut(declaration, ":")
		name = strings.TrimSpace(name)
		if !ok || !strings.HasPrefix(name, "--") {
			continue
		}
		tokens[name] = strings.TrimSpace(value)
	}
}

// mustColor возвращает цвет токена, разрешая цепочки var(). Неизвестный или
// непарсимый токен — ошибка теста, а не молчаливый пропуск.
func mustColor(t *testing.T, theme string, tokens map[string]string, name string) color {
	t.Helper()
	value, ok := tokens[name]
	if !ok {
		t.Fatalf("%s: токен %s не найден", theme, name)
	}
	return mustColorValue(t, theme, tokens, value)
}

// mustColorValue разрешает значение (возможно, var()) в цвет через карту токенов
// темы. Нужен для значений, вычитанных прямо из правила оформления.
func mustColorValue(t *testing.T, theme string, tokens map[string]string, value string) color {
	t.Helper()
	origin := value
	for depth := 0; depth < 10 && strings.HasPrefix(value, "var("); depth++ {
		inner := strings.TrimSuffix(strings.TrimPrefix(value, "var("), ")")
		reference, _, _ := strings.Cut(inner, ",")
		reference = strings.TrimSpace(reference)
		next, ok := tokens[reference]
		if !ok {
			t.Fatalf("%s: %s ссылается на неизвестный токен %s", theme, origin, reference)
		}
		value = next
	}
	parsed, ok := parseColor(value)
	if !ok {
		t.Fatalf("%s: значение %q (из %s) не является цветом", theme, value, origin)
	}
	return parsed
}

// cssBlockFor возвращает тело правила, в списке селекторов которого есть
// указанный селектор. Комментарии удаляются, поэтому поиск идёт по чистому CSS.
func cssBlockFor(t *testing.T, css, selector string) string {
	t.Helper()
	withoutComments := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	rule := regexp.MustCompile(`(?s)([^{}]*)\{([^{}]*)\}`)
	for _, match := range rule.FindAllStringSubmatch(withoutComments, -1) {
		for _, part := range strings.Split(match[1], ",") {
			if strings.TrimSpace(part) == selector {
				return match[2]
			}
		}
	}
	t.Fatalf("правило %s не найдено в оформлении", selector)
	return ""
}

// declarationValue возвращает значение свойства в теле правила; отсутствие
// свойства — ошибка теста.
func declarationValue(t *testing.T, selector, block, property string) string {
	t.Helper()
	for _, declaration := range strings.Split(block, ";") {
		name, value, ok := strings.Cut(declaration, ":")
		if ok && strings.TrimSpace(name) == property {
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("правило %s не задаёт %s", selector, property)
	return ""
}

// parseColor разбирает #rgb, #rrggbb, rgb() и rgba().
func parseColor(value string) (color, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") {
		return parseHex(value)
	}
	if strings.HasPrefix(value, "rgb(") || strings.HasPrefix(value, "rgba(") {
		return parseRGBA(value)
	}
	return color{}, false
}

// parseHex разбирает шестнадцатеричный цвет.
func parseHex(value string) (color, bool) {
	hex := strings.TrimPrefix(value, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return color{}, false
	}
	parsed, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return color{}, false
	}
	return color{
		r: float64((parsed>>16)&0xff) / 255,
		g: float64((parsed>>8)&0xff) / 255,
		b: float64(parsed&0xff) / 255,
		a: 1,
	}, true
}

// parseRGBA разбирает rgb()/rgba() с компонентами 0..255 и прозрачностью 0..1.
func parseRGBA(value string) (color, bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "rgba("), ")")
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "rgb("), ")")
	parts := strings.Split(inner, ",")
	if len(parts) < 3 {
		return color{}, false
	}
	channels := make([]float64, 0, 3)
	for _, part := range parts[:3] {
		channel, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return color{}, false
		}
		channels = append(channels, channel/255)
	}
	alpha := 1.0
	if len(parts) >= 4 {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err != nil {
			return color{}, false
		}
		alpha = parsed
	}
	return color{r: channels[0], g: channels[1], b: channels[2], a: alpha}, true
}

// composite накладывает цвет (возможно полупрозрачный) на непрозрачный фон.
func composite(foreground, background color) color {
	return color{
		r: foreground.r*foreground.a + background.r*(1-foreground.a),
		g: foreground.g*foreground.a + background.g*(1-foreground.a),
		b: foreground.b*foreground.a + background.b*(1-foreground.a),
		a: 1,
	}
}

// contrastRatio считает контраст по WCAG 2.1.
func contrastRatio(first, second color) float64 {
	luminanceFirst := relativeLuminance(first)
	luminanceSecond := relativeLuminance(second)
	high, low := luminanceFirst, luminanceSecond
	if low > high {
		high, low = low, high
	}
	return (high + 0.05) / (low + 0.05)
}

// relativeLuminance считает относительную яркость sRGB.
func relativeLuminance(value color) float64 {
	return 0.2126*linearize(value.r) + 0.7152*linearize(value.g) + 0.0722*linearize(value.b)
}

// linearize переводит компонент sRGB в линейный.
func linearize(channel float64) float64 {
	if channel <= 0.03928 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}
