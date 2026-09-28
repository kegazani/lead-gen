// Package forum — порт поиска форумных сигналов: поиск тем, болей и обсуждений
// на разрешённых форумах по запросу оператора.
//
// Порт выражен интерфейсом, а не конкретным SDK: потребитель держит только его и
// не зависит от внешнего источника. Реализация обязана уважать context.Context
// (дедлайн и отмену), допускать повторные вызовы и не протекать ошибками SDK
// наружу — ошибка провайдера возвращается как обычная ошибка Go, а не как тип
// из библиотеки поставщика.
package forum

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Port — порт поиска форумных сигналов. Реализуется адаптером форумного
// источника (в срезе — поиск, ограниченный доменами форумов).
type Port interface {
	// SearchSignals ищет на форумах темы, боли и обсуждения, относящиеся к
	// запросу, и возвращает их как обезличенные сигналы.
	SearchSignals(ctx context.Context, query Query) ([]Signal, error)
}

// Query — запрос оператора к поиску сигналов.
type Query struct {
	// Text — текст запроса. Обязателен.
	Text string
}

// Signal — один найденный сигнал: тема, выжимка, URL источника и дата. Сигнал
// не несёт прямых идентификаторов участников (имён, контактов): обезличивание
// либо исключение материала с идентификаторами — обязанность потребителя.
type Signal struct {
	// Topic — тема обсуждения.
	Topic string
	// Summary — выжимка обсуждения (суть боли или запроса).
	Summary string
	// SourceURL — адрес источника (страницы форума). Обязателен.
	SourceURL string
	// Date — дата сигнала (публикации либо сбора).
	Date time.Time
}

// Validate проверяет обязательные поля запроса.
func (q Query) Validate() error {
	if strings.TrimSpace(q.Text) == "" {
		return fmt.Errorf("forum: query: text обязателен")
	}
	return nil
}

// Validate проверяет обязательные поля сигнала: тема и адрес источника.
func (s Signal) Validate() error {
	var invalid []string
	if strings.TrimSpace(s.Topic) == "" {
		invalid = append(invalid, "topic")
	}
	if strings.TrimSpace(s.SourceURL) == "" {
		invalid = append(invalid, "source_url")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("forum: signal: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
