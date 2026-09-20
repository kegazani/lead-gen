package contracts

import "testing"

func TestRoutingKey(t *testing.T) {
	got, err := RoutingKey("approved-action", "1.4.2")
	if err != nil {
		t.Fatalf("RoutingKey: %v", err)
	}
	if got != "approved-action.1" {
		t.Fatalf("RoutingKey = %q, ожидалось %q", got, "approved-action.1")
	}

	// Разные MAJOR дают разные ключи — это и есть параллельное чтение версий.
	got, err = RoutingKey("approved-action", "2.0.0")
	if err != nil {
		t.Fatalf("RoutingKey: %v", err)
	}
	if got != "approved-action.2" {
		t.Fatalf("RoutingKey = %q, ожидалось %q", got, "approved-action.2")
	}
}

func TestRoutingKeyRejectsInvalidInput(t *testing.T) {
	if _, err := RoutingKey("", "1.0.0"); err == nil {
		t.Fatal("пустое имя должно быть ошибкой")
	}
	if _, err := RoutingKey("approved-action", "1.0"); err == nil {
		t.Fatal("некорректная версия должна быть ошибкой")
	}
}
