package documentparse

import "testing"

func TestRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{name: "валидный с форматом", req: Request{Source: "https://example.com/kp.pdf", Format: "pdf"}},
		{name: "валидный без формата", req: Request{Source: "https://example.com/kp"}},
		{name: "без источника", req: Request{Format: "pdf"}, wantErr: true},
		{name: "пробельный источник", req: Request{Source: "   "}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка валидации для %+v", tc.req)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestDocumentValidate(t *testing.T) {
	if err := (Document{Source: "https://example.com/kp.pdf", Text: "текст"}).Validate(); err != nil {
		t.Fatalf("валидный документ отвергнут: %v", err)
	}
	// Пустой текст допустим на уровне контракта: разбор без содержимого — честный
	// исход, а не ошибка схемы.
	if err := (Document{Source: "s", Text: ""}).Validate(); err != nil {
		t.Fatalf("документ без текста отвергнут: %v", err)
	}
	if err := (Document{Text: "текст"}).Validate(); err == nil {
		t.Fatal("документ без источника должен быть отвергнут")
	}
}
