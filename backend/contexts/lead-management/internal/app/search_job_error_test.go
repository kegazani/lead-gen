package app

import (
	"errors"
	"testing"

	"lead-gen/backend/platform/jobs"
)

// Проверка на этапе компиляции, что SearchJobError реализует jobs.CodedError.
var _ jobs.CodedError = (*SearchJobError)(nil)

func TestSearchJobErrorCodeAndMessage(t *testing.T) {
	cases := []struct {
		code    ErrorCode
		message string
	}{
		{ErrorCodePayloadInvalid, "Некорректный запрос"},
		{ErrorCodeBudgetExhausted, "Закончились кредиты"},
		{ErrorCodeSearchFailed, "Не удалось выполнить поиск"},
		{ErrorCodeCrawlFailed, "Не удалось обойти сайт"},
		{ErrorCodeParseFailed, "Страница не распознана"},
		{ErrorCodeInterrupted, "Задание прервано перезапуском"},
	}
	for _, tc := range cases {
		e := newSearchJobError(tc.code, errors.New("деталь"))
		if e.Code() != string(tc.code) {
			t.Fatalf("Code: получено %q, ожидалось %q", e.Code(), tc.code)
		}
		if e.Message() != tc.message {
			t.Fatalf("Message: получено %q, ожидалось %q", e.Message(), tc.message)
		}
	}
}

func TestSearchJobErrorRetainsDetailForErrorChain(t *testing.T) {
	detail := errors.New("postgres: таймаут 10s")
	e := newSearchJobError(ErrorCodeCrawlFailed, detail)
	if e.Error() != detail.Error() {
		t.Fatalf("Error: получено %q, ожидалась техническая деталь %q", e.Error(), detail.Error())
	}
	if !errors.Is(e, detail) {
		t.Fatal("Unwrap должен раскрывать техническую деталь")
	}
}
