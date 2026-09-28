package crawl

import "testing"

func TestRequestValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{name: "валидный", req: Request{StartURL: "https://example.com", MaxPages: 10, MaxDepth: 3, IncludePaths: []string{"contact|team|about"}, ExcludePaths: []string{"blog"}}},
		{name: "лимит времени не задан", req: Request{StartURL: "u", MaxTimeSeconds: 0}},
		{name: "положительный лимит времени", req: Request{StartURL: "u", MaxDepth: 2, MaxTimeSeconds: 90}},
		{name: "без стартового URL", req: Request{MaxPages: 10}, wantErr: true},
		{name: "отрицательный бюджет", req: Request{StartURL: "u", MaxPages: -1}, wantErr: true},
		{name: "отрицательная глубина", req: Request{StartURL: "u", MaxDepth: -1}, wantErr: true},
		{name: "отрицательный лимит времени", req: Request{StartURL: "u", MaxTimeSeconds: -1}, wantErr: true},
		{name: "пустой include-паттерн", req: Request{StartURL: "u", IncludePaths: []string{"  "}}, wantErr: true},
		{name: "пустой exclude-паттерн", req: Request{StartURL: "u", ExcludePaths: []string{""}}, wantErr: true},
		{name: "некорректный include-regex", req: Request{StartURL: "u", IncludePaths: []string{"*contact*"}}, wantErr: true},
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

func TestExtractRequestValidate(t *testing.T) {
	if err := (ExtractRequest{URL: "https://example.com"}).Validate(); err != nil {
		t.Fatalf("валидный запрос отвергнут: %v", err)
	}
	if err := (ExtractRequest{}).Validate(); err == nil {
		t.Fatal("запрос без URL должен быть отвергнут")
	}
}
