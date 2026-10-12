package httpapi

import "testing"

// TestPortalNavAnonymousUnit проверяет, что анонимный посетитель не получает
// рабочего бокового меню: состав пуст, а выбор входа и регистрации живёт в
// auth-карточке (authTabs).
func TestPortalNavAnonymousUnit(t *testing.T) {
	nav := portalNav("/login", false)
	if len(nav.Items) != 0 {
		t.Fatalf("пунктов анонима = %d, ожидалось 0", len(nav.Items))
	}
}

// TestPortalNavAuthenticatedUnit проверяет три пункта вошедшего клиента и
// отметку активного пункта.
func TestPortalNavAuthenticatedUnit(t *testing.T) {
	nav := portalNav("/portal", true)
	if len(nav.Items) != 3 {
		t.Fatalf("пунктов клиента = %d, ожидалось 3", len(nav.Items))
	}
	want := []struct {
		label string
		href  string
	}{
		{"Портал", "/portal"},
		{"Профиль", "/profile"},
		{"Заявка", "/portal/request"},
	}
	for i, w := range want {
		item := nav.Items[i]
		if item.Label != w.label || item.Href != w.href {
			t.Fatalf("пункт %d = {%q, %q}, ожидался {%q, %q}", i, item.Label, item.Href, w.label, w.href)
		}
	}
	for i, item := range nav.Items {
		wantActive := i == 0
		if item.Active != wantActive {
			t.Fatalf("пункт %d (%s): Active = %v, ожидалось %v", i, item.Href, item.Active, wantActive)
		}
	}
}
