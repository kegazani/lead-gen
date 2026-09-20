package deployment

import (
	"fmt"
	"strings"
)

// Этот файл содержит минимальный разбор YAML-подмножества, достаточного для
// `deploy/services.yaml` и `deploy/infrastructure.yaml`: вложенные отображения
// и блочные списки, flow-списки скаляров вида `[a, b]`, однострочные
// комментарии. Полноценный YAML не нужен — формат источников узок и фиксирован,
// а внешняя зависимость недопустима. Разбор возвращает нейтральное дерево
// (`map[string]any`, `[]any`, `string`), а строгая проверка ключей и приведение к
// типам выполняются вызывающим кодом.

// yamlLine — значимая строка YAML: отступ в пробелах и содержимое без
// комментария и внешних пробелов.
type yamlLine struct {
	indent int
	text   string
}

// yamlLines нормализует вход: отбрасывает пустые строки и комментарии, считает
// отступ. Отступ задаётся только пробелами; табуляция в отступе — ошибка, потому
// что её ширина неоднозначна и разные редакторы дают разный результат.
func yamlLines(data []byte) ([]yamlLine, error) {
	var lines []yamlLine
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r\t ")
		leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if strings.Contains(leading, "\t") {
			return nil, fmt.Errorf("строка %d: табуляция в отступе не поддерживается", index+1)
		}
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if marker := strings.Index(text, " #"); marker >= 0 {
			text = strings.TrimSpace(text[:marker])
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		lines = append(lines, yamlLine{indent: indent, text: text})
	}
	return lines, nil
}

// parseYAML разбирает документ в дерево значений. Пустой документ — пустое
// отображение. Лишние отступы и хвостовые строки — ошибка: молчаливый пропуск
// неразобранного скрыл бы опечатку в конфигурации.
func parseYAML(data []byte) (any, error) {
	lines, err := yamlLines(data)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return map[string]any{}, nil
	}
	node, next, err := parseBlock(lines, 0, 0)
	if err != nil {
		return nil, err
	}
	if next != len(lines) {
		return nil, fmt.Errorf("строка %d: неожиданное содержимое %q", next+1, lines[next].text)
	}
	return node, nil
}

// parseBlock читает блок (отображение или список) с отступом indent, начиная со
// строки index. minIndent — минимально допустимый отступ блока.
func parseBlock(lines []yamlLine, index, minIndent int) (any, int, error) {
	if index >= len(lines) {
		return nil, index, nil
	}
	indent := lines[index].indent
	if indent < minIndent {
		return nil, index, fmt.Errorf("строка %d: неожиданный отступ", index+1)
	}
	if strings.HasPrefix(lines[index].text, "-") {
		return parseSequence(lines, index, indent)
	}
	return parseMapping(lines, index, indent)
}

// parseMapping читает отображение «ключ: значение».
func parseMapping(lines []yamlLine, index, indent int) (any, int, error) {
	result := map[string]any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("строка %d: неожиданный отступ %q", index+1, line.text)
		}
		if strings.HasPrefix(line.text, "-") {
			break
		}
		key, raw, ok := splitKeyValue(line.text)
		if !ok || key == "" {
			return nil, index, fmt.Errorf("строка %d: ожидалась пара «ключ: значение», получено %q", index+1, line.text)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, index, fmt.Errorf("строка %d: повторный ключ %q", index+1, key)
		}
		value, next, err := parseValue(lines, index+1, indent, raw)
		if err != nil {
			return nil, index, err
		}
		result[key] = value
		index = next
	}
	return result, index, nil
}

// parseSequence читает блочный список. Элемент может быть скаляром, flow-списком
// или отображением, первый ключ которого стоит на строке с дефисом.
func parseSequence(lines []yamlLine, index, indent int) (any, int, error) {
	items := []any{}
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("строка %d: неожиданный отступ %q", index+1, line.text)
		}
		if !strings.HasPrefix(line.text, "-") {
			break
		}

		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		if rest == "" {
			if index+1 < len(lines) && lines[index+1].indent > indent {
				value, next, err := parseBlock(lines, index+1, indent+1)
				if err != nil {
					return nil, index, err
				}
				items = append(items, value)
				index = next
				continue
			}
			items = append(items, nil)
			index++
			continue
		}

		key, raw, isMapping := splitKeyValue(rest)
		if !isMapping {
			value, err := parseInlineScalar(rest)
			if err != nil {
				return nil, index, err
			}
			items = append(items, value)
			index++
			continue
		}

		// Элемент — отображение: первый ключ на строке с дефисом, остальные на
		// том же уровне отступа, что и «виртуальный» ключ после дефиса.
		itemIndent := indent + 2
		item := map[string]any{}
		value, next, err := parseValue(lines, index+1, itemIndent, raw)
		if err != nil {
			return nil, index, err
		}
		item[key] = value
		index = next

		for index < len(lines) {
			setting := lines[index]
			if setting.indent <= indent || strings.HasPrefix(setting.text, "-") {
				break
			}
			if setting.indent != itemIndent {
				return nil, index, fmt.Errorf("строка %d: неожиданный отступ %q", index+1, setting.text)
			}
			settingKey, settingRaw, ok := splitKeyValue(setting.text)
			if !ok || settingKey == "" {
				return nil, index, fmt.Errorf("строка %d: ожидалась пара «ключ: значение», получено %q", index+1, setting.text)
			}
			if _, duplicate := item[settingKey]; duplicate {
				return nil, index, fmt.Errorf("строка %d: повторный ключ %q", index+1, settingKey)
			}
			settingValue, nextSetting, err := parseValue(lines, index+1, itemIndent, settingRaw)
			if err != nil {
				return nil, index, err
			}
			item[settingKey] = settingValue
			index = nextSetting
		}
		items = append(items, item)
	}
	return items, index, nil
}

// parseValue читает значение ключа: непустое inline-значение (скаляр или
// flow-список) либо вложенный блок глубже отступом, чем строка ключа.
// keyIndent — отступ строки ключа.
func parseValue(lines []yamlLine, index, keyIndent int, raw string) (any, int, error) {
	if strings.TrimSpace(raw) != "" {
		value, err := parseInlineScalar(raw)
		if err != nil {
			return nil, index, err
		}
		return value, index, nil
	}
	if index < len(lines) && lines[index].indent > keyIndent {
		return parseBlock(lines, index, keyIndent+1)
	}
	return nil, index, nil
}

// parseInlineScalar разбирает значение на строке: flow-список или скаляр.
func parseInlineScalar(raw string) (any, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "[") {
		return parseFlowSequence(value)
	}
	return unquoteScalar(value), nil
}

// parseFlowSequence разбирает однострочный список скаляров `[a, b, c]`.
// Вложенные структуры не поддержаны: источники их не используют, а молчаливое
// упрощение исказило бы данные.
func parseFlowSequence(value string) ([]any, error) {
	if !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("flow-список %q не закрыт", value)
	}
	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return []any{}, nil
	}
	items := []any{}
	for _, part := range strings.Split(inner, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "[") || strings.HasPrefix(item, "{") {
			return nil, fmt.Errorf("вложенные структуры в flow-списке не поддержаны: %q", item)
		}
		items = append(items, unquoteScalar(item))
	}
	return items, nil
}

// splitKeyValue делит строку на ключ и значение по первому двоеточию вне кавычек.
func splitKeyValue(text string) (string, string, bool) {
	inSingle, inDouble := false, false
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':':
			if !inSingle && !inDouble {
				return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:]), true
			}
		}
	}
	return "", "", false
}

// unquoteScalar снимает необязательные обрамляющие кавычки.
func unquoteScalar(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			return trimmed[1 : len(trimmed)-1]
		}
	}
	return trimmed
}

// asMapping приводит значение к отображению.
func asMapping(value any) (map[string]any, error) {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ожидалось отображение, получено %T", value)
	}
	return mapping, nil
}

// asSequence приводит значение к списку.
func asSequence(value any) ([]any, error) {
	sequence, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("ожидался список, получено %T", value)
	}
	return sequence, nil
}

// asString приводит значение к скаляру-строке.
func asString(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("ожидалась строка, получено %T", value)
	}
	return text, nil
}

// asStringSequence приводит значение к списку строк. nil означает отсутствие
// списка, а не пустой список.
func asStringSequence(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	sequence, err := asSequence(value)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(sequence))
	for _, raw := range sequence {
		text, err := asString(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, text)
	}
	return items, nil
}

// rejectUnknownKeys проверяет, что отображение не содержит ключей вне allowed.
// Неизвестный ключ — ошибка: опечатка в конфигурации должна быть видна сразу.
func rejectUnknownKeys(mapping map[string]any, allowed ...string) error {
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	for key := range mapping {
		if !known[key] {
			return fmt.Errorf("неизвестное поле %q", key)
		}
	}
	return nil
}
