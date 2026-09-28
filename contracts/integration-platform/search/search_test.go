package search

import "testing"

func TestQueryValidate(t *testing.T) {
	cases := []struct {
		name    string
		query   Query
		wantErr bool
	}{
		{name: "валидный", query: Query{Text: "маркетинговые агентства", MaxResults: 10}},
		{name: "пустой текст", query: Query{MaxResults: 10}, wantErr: true},
		{name: "отрицательный лимит", query: Query{Text: "q", MaxResults: -1}, wantErr: true},
		{name: "нулевой лимит допустим", query: Query{Text: "q"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.query.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("ожидалась ошибка валидации для %+v", tc.query)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("не ожидалась ошибка: %v", err)
			}
		})
	}
}

func TestResultValidate(t *testing.T) {
	if err := (Result{URL: "https://example.com"}).Validate(); err != nil {
		t.Fatalf("валидный результат отвергнут: %v", err)
	}
	if err := (Result{}).Validate(); err == nil {
		t.Fatal("результат без URL должен быть отвергнут")
	}
}
