// Package vector — опубликованный (published language) контракт vector контекста
// integration-platform: векторное представление текста и поиск по близости.
//
// Векторное хранилище — внешняя зависимость за provider-port (`integration-platform`),
// как Firecrawl или LLM. Контракт не знает доменных правил: он не решает, что
// «релевантно», и не присваивает вердиктов — близость векторов это recall, а вердикт
// релевантности остаётся за lead-management (`lead-relevance`).
//
// Зависимости — только стандартная библиотека: контракт не импортирует ни
// внутренние пакеты владельца, ни platform.
package vector

import (
	"context"
	"errors"
)

// ContractName — имя публикуемого контракта.
const ContractName = "vector"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// ErrUnavailable — хранилище или эмбеддинг-сервис недоступны. Потребитель
// различает сбой транспорта и доменный отказ.
var ErrUnavailable = errors.New("vector: векторное хранилище недоступно")

// ErrRejected — запрос отклонён по причине владельца (например, пустой текст).
var ErrRejected = errors.New("vector: запрос отклонён")

// Vector — плотный вектор эмбеддинга. Значения — вещественные числа.
type Vector []float32

// Document — документ для индексации: идентификатор и текст.
type Document struct {
	// ID — идентификатор индексируемого объекта (например, компания).
	ID string
	// Text — текст, из которого строится эмбеддинг.
	Text string
}

// Match — найденный документ с оценкой близости. Чем больше Score, тем ближе;
// смысл оценки определяет владелец (косинусная близость), а не потребитель.
type Match struct {
	ID    string
	Score float64
}

// Port — векторное представление и поиск по близости. Реализация обращается к
// внешнему эмбеддинг-сервису и индексу; ошибки владельца нормализуются до
// ErrUnavailable/ErrRejected.
type Port interface {
	// Embed строит вектор для текста.
	Embed(ctx context.Context, text string) (Vector, error)

	// Upsert записывает документ в индекс и возвращает его вектор.
	Upsert(ctx context.Context, document Document) (Vector, error)

	// Search возвращает до k ближайших документов, упорядоченных по убыванию
	// близости. Пустой индекс или отсутствие совпадений даёт пустой список, а не
	// ошибку: recall может не найти кандидатов.
	Search(ctx context.Context, text string, k int) ([]Match, error)
}
