package emailgeneration_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/lead-management/emailgeneration"
)

// TestInProcDelegates проверяет, что прямой адаптер передаёт запрос обработчику и
// возвращает его результат.
func TestInProcDelegates(t *testing.T) {
	var got emailgeneration.Request
	port := emailgeneration.InProc(emailgeneration.InProcHandler{
		GenerateFunc: func(_ context.Context, request emailgeneration.Request) (emailgeneration.Email, error) {
			got = request
			return emailgeneration.Email{Subject: "Тема", Body: "Тело", Language: "ru"}, nil
		},
	})

	email, err := port.Generate(context.Background(), emailgeneration.Request{
		RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead.example",
		Criteria: "оффер: автоматизация продаж", Language: "ru", Tone: "деловой",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.LeadID != "lead.example" || got.Criteria != "оффер: автоматизация продаж" || got.RequestID != "req-1" {
		t.Fatalf("обработчик получил %+v", got)
	}
	if email.Subject != "Тема" || email.Body != "Тело" || email.Language != "ru" {
		t.Fatalf("письмо = %+v", email)
	}
}

// TestInProcNilHandlerUnavailable проверяет, что незаданный обработчик честно
// сообщает о недоступности владельца, а не выдаёт пустое письмо.
func TestInProcNilHandlerUnavailable(t *testing.T) {
	port := emailgeneration.InProc(emailgeneration.InProcHandler{})
	if _, err := port.Generate(context.Background(), emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, emailgeneration.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestInProcPropagatesDomainErrors проверяет, что доменный отказ владельца
// доходит до потребителя без подмены успехом.
func TestInProcPropagatesDomainErrors(t *testing.T) {
	port := emailgeneration.InProc(emailgeneration.InProcHandler{
		GenerateFunc: func(context.Context, emailgeneration.Request) (emailgeneration.Email, error) {
			return emailgeneration.Email{}, emailgeneration.ErrRejected
		},
	})
	if _, err := port.Generate(context.Background(), emailgeneration.Request{LeadID: "x", Criteria: "y"}); !errors.Is(err, emailgeneration.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestRequestValidate проверяет обязательные поля запроса.
func TestRequestValidate(t *testing.T) {
	if err := (emailgeneration.Request{LeadID: "lead.example", Criteria: "оффер"}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (emailgeneration.Request{Criteria: "оффер"}).Validate(); err == nil {
		t.Fatal("пустой lead_id не отвергнут")
	}
	if err := (emailgeneration.Request{LeadID: "lead.example"}).Validate(); err == nil {
		t.Fatal("пустой criteria не отвергнут")
	}
}
