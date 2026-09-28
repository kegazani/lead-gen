// Package mail — порт отправки письма: доставка собранного сообщения получателю.
//
// Порт выражен интерфейсом, а не конкретным SMTP-клиентом: потребитель держит
// только его и не зависит от транспорта доставки. Реализация обязана уважать
// context.Context (дедлайн и отмену), допускать повторные вызовы и не протекать
// ошибками SDK/протокола наружу — ошибка провайдера возвращается как обычная
// ошибка Go, а не как тип из библиотеки поставщика.
package mail

import (
	"context"
	"fmt"
	"strings"
)

// Port — порт отправки письма. Реализуется адаптером доставки (в срезе — SMTP).
type Port interface {
	// Send доставляет сообщение получателю. Возвращает ошибку только при отказе
	// доставки; nil означает, что сообщение принято к отправке.
	Send(ctx context.Context, message Message) error
}

// Message — собранное письмо, готовое к доставке.
type Message struct {
	// From — адрес отправителя. Обязателен.
	From string
	// To — адрес получателя. Обязателен.
	To string
	// Subject — тема письма.
	Subject string
	// Body — тело письма.
	Body string
}

// Validate проверяет обязательные поля сообщения и называет все нарушения разом.
func (m Message) Validate() error {
	var invalid []string
	if strings.TrimSpace(m.From) == "" {
		invalid = append(invalid, "from")
	}
	if strings.TrimSpace(m.To) == "" {
		invalid = append(invalid, "to")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("mail: message: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
