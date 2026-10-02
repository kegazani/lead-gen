package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Material — подготовленный под площадку материал: заголовок (необязательный) и
// текст. Материал не является самостоятельным value object каталога: он входит в
// состав агрегата publication.
//
// Текст ограничен форматом площадки. При превышении предела материал сокращается
// детерминированно — текст обрезается по границе руны и получает признак
// сокращения, чтобы сценарий мог сообщить оператору о сокращении, а не молча
// усечь материал.
type Material struct {
	title     string
	body      string
	shortened bool
}

// MaterialSnapshot — сохранённое состояние материала.
type MaterialSnapshot struct {
	Title     string
	Body      string
	Shortened bool
}

// NewMaterial собирает материал под цель публикации. Пустой после обрезки пробелов
// текст отвергается. Текст длиннее предела площадки сокращается с признаком
// Shortened.
func NewMaterial(target PublicationTarget, title, body string) (Material, error) {
	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return Material{}, fmt.Errorf("%w: %s", ErrEmptyMaterial, target.Platform())
	}
	limit := target.Platform().MaxBody()
	shortened := false
	if limit > 0 && utf8.RuneCountInString(trimmedBody) > limit {
		trimmedBody = truncateRunes(trimmedBody, limit)
		shortened = true
	}
	return Material{
		title:     strings.TrimSpace(title),
		body:      trimmedBody,
		shortened: shortened,
	}, nil
}

// Title возвращает заголовок материала.
func (m Material) Title() string { return m.title }

// Body возвращает текст материала.
func (m Material) Body() string { return m.body }

// Shortened сообщает, был ли текст сокращён под предел площадки.
func (m Material) Shortened() bool { return m.shortened }

// Snapshot возвращает состояние материала для сохранения.
func (m Material) Snapshot() MaterialSnapshot {
	return MaterialSnapshot{Title: m.title, Body: m.body, Shortened: m.shortened}
}

// rehydrateMaterial восстанавливает материал из снимка, перепроверяя непустоту.
func rehydrateMaterial(target PublicationTarget, s MaterialSnapshot) (Material, error) {
	if strings.TrimSpace(s.Body) == "" {
		return Material{}, fmt.Errorf("%w", ErrEmptyMaterial)
	}
	return Material{title: s.Title, body: s.Body, shortened: s.Shortened}, nil
}

// truncateRunes усекает текст до limit рун. Многоточие входит в предел: текст
// остаётся в границах площадки, а не выходит за них на его длину.
func truncateRunes(s string, limit int) string {
	if limit <= 1 {
		return string([]rune(s)[:limit])
	}
	runes := []rune(s)
	return string(runes[:limit-1]) + "…"
}
