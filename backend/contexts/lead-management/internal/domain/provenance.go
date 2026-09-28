// Package domain — доменная модель контекста lead-management: агрегаты
// lead-profile (AR company) и contact (AR contact), происхождение значений и
// доменные события.
//
// Это минимальный срез агрегатов каталога architecture/domain-catalog.yaml.
// Адаптеры не видят закрытые поля агрегатов: они читают и пишут снимки
// (snapshot.go), а восстановление проверяет инварианты.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// Provenance — происхождение значения поля карточки компании: URL источника и
// дата извлечения. Каждое внешнее значение обязано иметь происхождение.
type Provenance struct {
	// SourceURL — URL страницы-источника.
	SourceURL string
	// ExtractedAt — дата извлечения.
	ExtractedAt time.Time
}

// validate проверяет полноту происхождения. Нулевое происхождение допустимо
// только для отсутствующего значения; для заполненного — обязательно.
func (p Provenance) validate() error {
	if strings.TrimSpace(p.SourceURL) == "" {
		return fmt.Errorf("%w: отсутствует URL источника", ErrMissingProvenance)
	}
	if p.ExtractedAt.IsZero() {
		return fmt.Errorf("%w: отсутствует дата извлечения", ErrMissingProvenance)
	}
	return nil
}

// ExtractionMethod — способ извлечения значения атрибута контакта.
type ExtractionMethod string

const (
	// MethodMarkdown — значение взято из метаданных/markdown страницы.
	MethodMarkdown ExtractionMethod = "markdown"
	// MethodRegex — значение извлечено regex-нормализацией текста.
	MethodRegex ExtractionMethod = "regex"
	// MethodLLM — значение извлечено structured-экстракцией (Firecrawl extract).
	MethodLLM ExtractionMethod = "llm"
)

// validMethod сообщает, известен ли способ извлечения.
func validMethod(m ExtractionMethod) bool {
	switch m {
	case MethodMarkdown, MethodRegex, MethodLLM:
		return true
	default:
		return false
	}
}

// AttributeProvenance — происхождение значения атрибута контакта: источник,
// дата и способ извлечения (entity attribute-provenance каталога).
type AttributeProvenance struct {
	// SourceURL — URL страницы-источника.
	SourceURL string
	// ExtractedAt — дата извлечения.
	ExtractedAt time.Time
	// Method — способ извлечения.
	Method ExtractionMethod
}

// validate проверяет полноту происхождения атрибута.
func (p AttributeProvenance) validate() error {
	if strings.TrimSpace(p.SourceURL) == "" {
		return fmt.Errorf("%w: отсутствует URL источника", ErrMissingProvenance)
	}
	if p.ExtractedAt.IsZero() {
		return fmt.Errorf("%w: отсутствует дата извлечения", ErrMissingProvenance)
	}
	if !validMethod(p.Method) {
		return fmt.Errorf("%w: неизвестный способ извлечения %q", ErrMissingProvenance, p.Method)
	}
	return nil
}
