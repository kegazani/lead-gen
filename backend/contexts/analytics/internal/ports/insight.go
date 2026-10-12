package ports

import (
	"context"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// InsightStore — порт хранилища снимков инсайтов качества поиска и
// зафиксированных применений рекомендаций.
//
// Проекция — витрина, а не источник истины: порт сохраняет снимок (исходы,
// watermark, версия формул) и возвращает сохранённые снимки для тёплого старта,
// поэтому срез воспроизводим. Сигнатуры оперируют снимками модели и не знают о
// драйвере СУБД, схеме таблиц и SQL. Запись снимка идемпотентна по тенанту:
// повторное сохранение заменяет прежний снимок, а не создаёт второй.
type InsightStore interface {
	// SaveSnapshot сохраняет снимок инсайтов тенанта. Повторный вызов для того же
	// тенанта заменяет прежний снимок (upsert по тенанту).
	SaveSnapshot(ctx context.Context, snapshot readmodel.InsightSnapshot) error

	// LoadSnapshots возвращает последние сохранённые снимки всех тенантов. Пустое
	// хранилище даёт пустой список без ошибки.
	LoadSnapshots(ctx context.Context) ([]readmodel.InsightSnapshot, error)

	// SaveApplication фиксирует факт применения рекомендации. Повтор по тому же
	// ключу (тенант, срез, время) заменяет прежнюю запись, а не создаёт вторую.
	SaveApplication(ctx context.Context, application readmodel.RecommendationApplication) error

	// LoadApplications возвращает зафиксированные применения рекомендаций.
	LoadApplications(ctx context.Context) ([]readmodel.RecommendationApplication, error)
}

// RecommendationChange — предлагаемое рекомендацией СТРУКТУРНОЕ изменение в
// контуре поиска `lead-management`. Не проза: вид изменения, критерий, цели
// обхода и лимит заданы явными полями, а не свободным текстом.
type RecommendationChange struct {
	// TenantID — тенант-владелец пула.
	TenantID string
	// ScopeKind — измерение рекомендации: job, source или industry.
	ScopeKind string
	// ScopeKey — значение измерения.
	ScopeKey string
	// Reason — категория причины, на которой основана рекомендация.
	Reason string
	// Basis — наблюдаемое основание рекомендации.
	Basis string
	// Change — структурное изменение контура поиска.
	Change SearchChange
}

// SearchChange — структурное изменение контура поиска, применяемое владельцем.
// Вид изменения, критерий, цели обхода и лимит заданы явными полями.
type SearchChange struct {
	// Kind — вид изменения; сейчас единственный — new-search (новый запуск
	// поиска с рекомендованными критериями).
	Kind string
	// Criteria — рекомендованный критерий поиска (текст запроса).
	Criteria string
	// Targets — рекомендованные цели обхода (поисковые запросы).
	Targets []string
	// Limit — рекомендованный целевой лимит компаний; 0 — по умолчанию владельца.
	Limit int
}

// RecommendationReceipt — подтверждение созданного в контуре поиска изменения.
type RecommendationReceipt struct {
	// Reference — непрозрачная ссылка на созданное изменение (идентификатор нового
	// задания поиска). Пустая ссылка означает, что владелец не подтвердил создание
	// изменения.
	Reference string
}

// QualityFeedback — порт применения рекомендации в контуре поиска
// (`lead-management`: capability search-recommendation).
//
// Реализация создаёт изменение в контуре поиска — новый запуск поиска с
// рекомендованными критериями (контракт recommendation-apply) — и возвращает
// ссылку на созданное задание. Отсутствие реализации — честная недоступность:
// применение не выдумывается, а фиксируется как недоступное (`AGENTS.md` §3).
type QualityFeedback interface {
	// ApplyRecommendation создаёт изменение в контуре поиска. Ошибка означает, что
	// изменение не создано.
	ApplyRecommendation(ctx context.Context, change RecommendationChange) (RecommendationReceipt, error)
}
