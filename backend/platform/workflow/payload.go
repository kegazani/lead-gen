package workflow

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Rule — правило payload-гигиены workflow и activities.
//
// В аргументы и результаты workflow/activities допускаются только
// идентификаторы, коды и корреляционные ссылки. Сырые персональные данные
// (email, телефон, ФИО, адрес, документы) и секреты (токены, пароли, ключи) в
// payload не попадают: они живут в owning context и передаются по id. Правило
// согласовано с data-governance (красные данные не поднимаются) и
// integration-platform (секреты не в событиях). Текстовые поля, если они нужны,
// несут короткий контролируемый код, а не свободный пользовательский текст.
const Rule = "payload workflow/activities содержит только идентификаторы, коды и корреляционные ссылки; PII и секреты — по id"

// forbiddenFieldMarkers — подстроки имён полей, указывающие на PII или секрет.
// Имя поля сравнивается без учёта регистра и разделителей подчёркивания.
var forbiddenFieldMarkers = []string{
	"email", "phone", "fullname", "firstname", "lastname", "address",
	"passport", "ssn", "birth", "token", "secret", "password", "passwd",
	"apikey", "credential", "iban", "cvv", "cardnumber", "contactname",
}

// Violation — одно нарушение payload-гигиены: поле и причина.
type Violation struct {
	Field  string
	Reason string
}

// CheckIdentifiersOnly проверяет тип payload на соответствие правилу
// Rule и возвращает найденные нарушения. Проверка статическая (по типу, не по
// значениям): она ловит PII/секреты в структуре payload и непрозрачные типы,
// которые могут спрятать что угодно. Пустой результат — payload корректен.
//
// Функция применяется на ревью и в тестах: зарегистрированные payload-типы
// контекста обязаны проходить её без нарушений.
func CheckIdentifiersOnly(payload any) []Violation {
	if payload == nil {
		return []Violation{{Field: "<payload>", Reason: "payload отсутствует"}}
	}
	var violations []Violation
	walkPayload(reflect.TypeOf(payload), "", map[reflect.Type]bool{}, &violations)
	return violations
}

// walkPayload обходит тип payload и накапливает нарушения. visited защищает от
// бесконечной рекурсии на самоссылающихся типах.
func walkPayload(t reflect.Type, path string, visited map[reflect.Type]bool, out *[]Violation) {
	if t == nil {
		return
	}

	switch t.Kind() {
	case reflect.Pointer:
		walkPayload(t.Elem(), path, visited, out)
		return
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			appendViolation(out, path, "непрозрачный тип []byte: может нести произвольные данные")
			return
		}
		walkPayload(t.Elem(), path+"[]", visited, out)
		return
	case reflect.Map, reflect.Interface, reflect.Func, reflect.Chan:
		appendViolation(out, path, fmt.Sprintf("непрозрачный тип %s: может спрятать PII или секрет", t.Kind()))
		return
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return
		}
		if visited[t] {
			return
		}
		visited[t] = true
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			fieldPath := joinPath(path, field.Name)
			if marker, found := forbiddenMarker(field.Name); found {
				appendViolation(out, fieldPath, fmt.Sprintf("имя поля указывает на PII или секрет (%q)", marker))
				continue
			}
			walkPayload(field.Type, fieldPath, visited, out)
		}
		return
	default:
		// Строки, числа, булевы значения и именованные типы на них — допустимы.
		return
	}
}

// forbiddenMarker возвращает первую запрещённую подстроку в имени поля.
func forbiddenMarker(name string) (string, bool) {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	for _, marker := range forbiddenFieldMarkers {
		if strings.Contains(normalized, marker) {
			return marker, true
		}
	}
	return "", false
}

// joinPath склеивает путь поля для сообщения о нарушении.
func joinPath(parent, field string) string {
	if parent == "" {
		return field
	}
	return parent + "." + field
}

// appendViolation добавляет нарушение в список.
func appendViolation(out *[]Violation, field, reason string) {
	*out = append(*out, Violation{Field: field, Reason: reason})
}
