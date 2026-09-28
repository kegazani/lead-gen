// Package search — порт поиска: поиск по запросу оператора и возврат найденных
// страниц с заголовком и сниппетом.
//
// Порт выражен интерфейсом, а не конкретным SDK: потребитель держит только его и
// не зависит от внешнего поисковика. Реализация обязана уважать context.Context
// (дедлайн и отмену), допускать повторные вызовы и не протекать ошибками SDK
// наружу — ошибка провайдера возвращается как обычная ошибка Go, а не как тип
// из библиотеки поставщика.
package search

import (
	"context"
	"fmt"
	"strings"
)

// Port — порт поиска. Реализуется адаптером внешнего поисковика
// (в срезе — Firecrawl /search).
type Port interface {
	// Search выполняет поиск по запросу и возвращает найденные страницы.
	Search(ctx context.Context, query Query) ([]Result, error)
}

// Query — поисковый запрос оператора.
type Query struct {
	// Text — текст запроса. Обязателен.
	Text string
	// MaxResults — верхняя граница числа результатов. Ноль означает значение
	// по умолчанию адаптера; отрицательное значение запрещено.
	MaxResults int
}

// Result — один найденный результат.
type Result struct {
	// URL — адрес найденной страницы. Обязателен.
	URL string
	// Title — заголовок страницы.
	Title string
	// Snippet — сниппет страницы (markdown).
	Snippet string
}

// Validate проверяет обязательные поля запроса и называет все нарушения разом.
func (q Query) Validate() error {
	var invalid []string
	if strings.TrimSpace(q.Text) == "" {
		invalid = append(invalid, "text")
	}
	if q.MaxResults < 0 {
		invalid = append(invalid, "max_results")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("search: query: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Validate проверяет обязательные поля результата.
func (r Result) Validate() error {
	if strings.TrimSpace(r.URL) == "" {
		return fmt.Errorf("search: result: URL обязателен")
	}
	return nil
}
