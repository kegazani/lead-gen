// Package documentparse — порт разбора предоставленного документа (коммерческого
// предложения) движком сбора данных.
//
// В отличие от порта crawl (обход сайта и структурированная экстракция контактов),
// этот порт отвечает за извлечение текста одного документа по его адресу: движок
// разбирает документ в поддерживаемом формате (например, PDF) и возвращает чистое
// текстовое содержимое. DTO фиксируют понятие источника и подсказки формата, а не
// конфигурацию конкретного движка: замена движка — правка адаптера, не ядра.
//
// Реализация обязана уважать context.Context, допускать повторные вызовы и не
// протекать ошибками SDK наружу.
package documentparse

import (
	"context"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта.
const ContractName = "document-parse"

// SchemaVersion — текущая версия схемы контракта (semver). Аддитивные изменения
// повышают MINOR, несовместимые — MAJOR.
const SchemaVersion = "1.0.0"

// Port — порт разбора документа. Реализуется адаптером движка сбора данных
// (в срезе — Firecrawl /v1/scrape в режиме markdown).
type Port interface {
	// ParseDocument разбирает документ по его источнику и возвращает извлечённый
	// текст. Неподдерживаемый формат или недоступный источник возвращаются
	// обычной ошибкой Go, а не пустым успехом.
	ParseDocument(ctx context.Context, request Request) (Document, error)
}

// Request — параметры разбора документа.
type Request struct {
	// Source — источник документа: адрес, по которому движок может его получить.
	// Обязателен.
	Source string
	// Format — подсказка формата документа (например, pdf). Необязательна: пустое
	// значение означает, что формат определяет движок.
	Format string
}

// Document — разобранный документ.
type Document struct {
	// Source — источник документа. Обязателен.
	Source string
	// Text — извлечённый текст документа. Может быть пустым: движок разобрал
	// документ, но текста в нём нет; пустой текст не выдаётся за содержимое.
	Text string
	// Format — формат, в котором документ был разобран. Необязателен.
	Format string
	// Metadata — метаданные документа (заголовок, разметка). Необязательны.
	Metadata map[string]string
}

// Validate проверяет обязательные поля запроса.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("documentparse: request: source обязателен")
	}
	return nil
}

// Validate проверяет обязательные поля документа.
func (d Document) Validate() error {
	if strings.TrimSpace(d.Source) == "" {
		return fmt.Errorf("documentparse: document: source обязателен")
	}
	return nil
}
