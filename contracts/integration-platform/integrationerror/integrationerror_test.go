package integrationerror

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Набор категорий закрыт: все объявленные категории валидны, дубликатов нет,
// произвольное значение отвергается.
func TestCategoriesAreClosedSet(t *testing.T) {
	categories := AllCategories()
	if len(categories) != 6 {
		t.Fatalf("категорий %d, ожидалось 6", len(categories))
	}
	seen := make(map[Category]struct{}, len(categories))
	for _, category := range categories {
		if !IsValidCategory(category) {
			t.Errorf("объявленная категория %q считается невалидной", category)
		}
		if _, dup := seen[category]; dup {
			t.Errorf("категория %q объявлена дважды", category)
		}
		seen[category] = struct{}{}
	}
	if IsValidCategory("teapot") || IsValidCategory("") {
		t.Error("неизвестная категория не должна проходить проверку")
	}

	// AllCategories отдаёт копию: изменение результата не портит набор.
	got := AllCategories()
	got[0] = "подмена"
	if !IsValidCategory(CategoryTemporarilyUnavailable) {
		t.Error("набор категорий изменён извне")
	}
}

// Значения категорий совпадают с набором контракта (проверка литералов).
func TestCategoryValues(t *testing.T) {
	want := map[Category]string{
		CategoryTemporarilyUnavailable: "temporarily-unavailable",
		CategoryRateLimited:            "rate-limited",
		CategoryAuthorizationDenied:    "authorization-denied",
		CategoryInvalidRequest:         "invalid-request",
		CategoryConflict:               "conflict",
		CategoryPermanentFailure:       "permanent-failure",
	}
	for category, literal := range want {
		if string(category) != literal {
			t.Errorf("категория: got %q, want %q", category, literal)
		}
	}
}

// ParseCategory нормализует регистр и пробелы и отвергает неизвестное имя.
func TestParseCategory(t *testing.T) {
	got, err := ParseCategory("  RATE-LIMITED ")
	if err != nil {
		t.Fatalf("ParseCategory: %v", err)
	}
	if got != CategoryRateLimited {
		t.Fatalf("ParseCategory: got %q, want %q", got, CategoryRateLimited)
	}
	if _, err := ParseCategory("nope"); err == nil {
		t.Fatal("неизвестная категория разобрана без ошибки")
	}
}

func validError() Error {
	return Error{
		Category:     CategoryTemporarilyUnavailable,
		AdapterName:  "firecrawl",
		ContractName: "provider-port",
		Reason:       "таймаут соединения",
	}
}

// Полная ошибка валидна, сериализация переживает round-trip без потерь.
func TestError_RoundTrip(t *testing.T) {
	orig := validError()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходная ошибка должна быть валидна: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Error
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Валидация называет все некорректные поля разом, а не первое из них.
func TestError_ValidateReportsAllInvalidFields(t *testing.T) {
	err := Error{}.Validate()
	if err == nil {
		t.Fatal("пустая ошибка должна быть невалидна")
	}
	for _, field := range []string{"category", "adapter_name", "contract_name"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Неизвестная категория отвергается валидацией.
func TestError_ValidateRejectsUnknownCategory(t *testing.T) {
	invalid := validError()
	invalid.Category = "teapot"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "category") {
		t.Fatalf("неизвестная категория должна отвергаться, got %v", err)
	}
}

// Контракт ведёт себя как обычная ошибка Go и не теряет причину.
func TestError_AsError(t *testing.T) {
	var err error = validError()
	if !strings.Contains(err.Error(), "temporarily-unavailable") {
		t.Fatalf("текст ошибки без категории: %v", err)
	}
	if !strings.Contains(err.Error(), "таймаут соединения") {
		t.Fatalf("текст ошибки без причины: %v", err)
	}

	var target Error
	if !errors.As(err, &target) {
		t.Fatal("errors.As не распознал контракт")
	}
	if target.AdapterName != "firecrawl" {
		t.Fatalf("адаптер: got %q", target.AdapterName)
	}
}
