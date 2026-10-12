// Package recommendationapply — опубликованный контракт recommendation-apply
// контекста lead-management: применение рекомендации анализа качества поиска как
// нового запуска поиска.
//
// Владелец контура поиска — lead-management (architecture/domain-catalog.yaml):
// цели обхода, источники и лимиты живут в его операционной записи задания
// (`search_job`), изменяемого агрегата политики поиска у него нет. Поэтому
// применение рекомендации выражается честно — **новым запуском поиска** с
// рекомендованными критериями, а не правкой несуществующей сущности. Потребитель
// (analytics, capability lead-quality-insights) формулирует структурное изменение
// и получает идентификатор созданного задания — непрозрачную ссылку на применение.
//
// # Идемпотентность
//
// Запрос несёт ключ идемпотентности ChangeID. Повтор с тем же ключом (в том же
// тенанте) не создаёт второе задание: владелец возвращает идентификатор уже
// созданного. Ключ детерминированно задаёт идентификатор задания, а уникальность
// идентификатора — арбитр; второго хранилища ключей не заводится.
//
// # Содержимое
//
// Контракт несёт только структурное изменение (вид, критерий, цели обхода, лимит)
// и ссылку на рекомендацию (измерение, значение, причина, основание). Никаких
// персональных данных и содержимого красной зоны здесь нет.
//
// # Транспорт
//
// Форма контракта не зависит от транспорта: при co-location вызов прямой
// (InProc), при разделении сервисов — сетевой клиент (recommendationapply/
// httpport, POST {base}/recommendation-apply). Wire-форма — snake_case (json-теги
// ниже), одинаковая у владельца и клиента.
//
// Зависимости — только стандартная библиотека.
package recommendationapply

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "recommendation-apply"

// SchemaVersion — текущая версия схемы контракта (semver).
const SchemaVersion = "1.0.0"

// Вид изменения. Набор закрыт: применение рекомендации выражается новым запуском
// поиска, а не правкой несуществующего агрегата политики.
const (
	// KindNewSearch — новый запуск поиска с рекомендованными критериями.
	KindNewSearch = "new-search"
)

// ErrRejected — применение отклонено по доменной причине владельца (пустой
// критерий, неизвестный вид изменения, отрицательный лимит). Потребитель
// различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("recommendationapply: применение отклонено владельцем")

// ErrUnavailable — владелец поиска недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успешное применение.
var ErrUnavailable = errors.New("recommendationapply: владелец поиска недоступен")

// Change — структурное изменение контура поиска. Не проза: вид, критерий, цели
// обхода и лимит заданы явными полями.
type Change struct {
	// Kind — вид изменения; сейчас единственный — KindNewSearch.
	Kind string `json:"kind"`
	// Criteria — рекомендованный критерий поиска (текст запроса).
	Criteria string `json:"criteria,omitempty"`
	// Targets — рекомендованные цели обхода (поисковые запросы).
	Targets []string `json:"targets,omitempty"`
	// Limit — рекомендованный целевой лимит компаний; 0 — лимит владельца по
	// умолчанию, отрицательное значение запрещено.
	Limit int `json:"limit,omitempty"`
}

// Validate проверяет структурную корректность изменения и называет все нарушения
// разом.
func (c Change) Validate() error {
	var invalid []string
	if strings.TrimSpace(c.Kind) != KindNewSearch {
		invalid = append(invalid, "kind")
	}
	if strings.TrimSpace(c.Criteria) == "" && len(trimmed(c.Targets)) == 0 {
		invalid = append(invalid, "criteria")
	}
	if c.Limit < 0 {
		invalid = append(invalid, "limit")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("recommendationapply: change: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Application — запрос применения рекомендации.
type Application struct {
	// TenantID — тенант-владелец контура поиска.
	TenantID string `json:"tenant_id"`
	// ChangeID — ключ идемпотентности применения: повтор с тем же ключом в том же
	// тенанте возвращает уже созданное задание, а не создаёт второе.
	ChangeID string `json:"change_id"`
	// ScopeKind — измерение рекомендации: job, source или industry.
	ScopeKind string `json:"scope_kind,omitempty"`
	// ScopeKey — значение измерения.
	ScopeKey string `json:"scope_key,omitempty"`
	// Reason — категория причины, на которой основана рекомендация.
	Reason string `json:"reason,omitempty"`
	// Basis — наблюдаемое основание рекомендации.
	Basis string `json:"basis,omitempty"`
	// Change — структурное изменение контура поиска.
	Change Change `json:"change"`
}

// Validate проверяет обязательные поля запроса и структурную корректность
// изменения.
func (a Application) Validate() error {
	var invalid []string
	if strings.TrimSpace(a.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(a.ChangeID) == "" {
		invalid = append(invalid, "change_id")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("recommendationapply: application: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return a.Change.Validate()
}

// Receipt — подтверждение созданного изменения: идентификатор задания поиска.
type Receipt struct {
	// JobID — идентификатор созданного (или ранее созданного по тому же ключу)
	// задания поиска. Пустой идентификатор означает, что владелец не подтвердил
	// создание изменения.
	JobID string `json:"job_id"`
}

// Port — команда применения рекомендации владельцу. Реализация создаёт новое
// задание поиска (идемпотентно по ChangeID) и возвращает ссылку на него;
// ErrRejected — доменный отказ, ErrUnavailable — недоступность владельца.
type Port interface {
	ApplyRecommendation(ctx context.Context, application Application) (Receipt, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Незаданная функция честно сообщает о недоступности владельца, а не выдумывает
// применение.
type InProcHandler struct {
	// ApplyFunc реализует применение.
	ApplyFunc func(ctx context.Context, application Application) (Receipt, error)
}

// ApplyRecommendation реализует Port. Незаданная функция честно сообщает о
// недоступности владельца.
func (h InProcHandler) ApplyRecommendation(ctx context.Context, application Application) (Receipt, error) {
	if h.ApplyFunc == nil {
		return Receipt{}, ErrUnavailable
	}
	return h.ApplyFunc(ctx, application)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }

// trimmed возвращает непустые строки среза, сохраняя порядок.
func trimmed(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
