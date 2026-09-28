package forum

import "testing"

func TestQueryValidate(t *testing.T) {
	if err := (Query{Text: "боли владельцев кофеен"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (Query{}).Validate(); err == nil {
		t.Fatal("запрос без text должен быть отвергнут")
	}
	if err := (Query{Text: "   "}).Validate(); err == nil {
		t.Fatal("запрос с пробелами должен быть отвергнут")
	}
}

func TestSignalValidate(t *testing.T) {
	cases := []struct {
		name    string
		signal  Signal
		wantErr bool
	}{
		{name: "полный", signal: Signal{Topic: "Тема", SourceURL: "https://example.com/t/1"}},
		{name: "без темы", signal: Signal{SourceURL: "https://example.com/t/1"}, wantErr: true},
		{name: "без источника", signal: Signal{Topic: "Тема"}, wantErr: true},
		{name: "пустой", signal: Signal{}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.signal.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка для %+v", tc.signal)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}
