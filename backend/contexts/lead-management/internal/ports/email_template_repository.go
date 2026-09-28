package ports

import (
	"context"
	"errors"
	"sort"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// ErrTemplateNotFound — шаблон письма не найден.
var ErrTemplateNotFound = errors.New("ports: шаблон письма не найден")

// EmailTemplateRepository — репозиторий шаблонов письма (зелёная зона).
type EmailTemplateRepository interface {
	// Save сохраняет шаблон (создаёт или заменяет по идентификатору).
	Save(ctx context.Context, template *domain.EmailTemplate) error
	// FindByID возвращает шаблон по идентификатору; отсутствие даёт
	// ports.ErrTemplateNotFound.
	FindByID(ctx context.Context, id domain.TemplateID) (*domain.EmailTemplate, error)
	// Delete удаляет шаблон по идентификатору; отсутствие даёт
	// ports.ErrTemplateNotFound.
	Delete(ctx context.Context, id domain.TemplateID) error
	// Search ищет шаблоны по нормализованному запросу: по имени, теме, телу и
	// объявленным переменным. Пустой запрос возвращает все шаблоны; результат
	// упорядочен по имени, затем по идентификатору; limit <= 0 — без предела.
	Search(ctx context.Context, query string, limit int) ([]*domain.EmailTemplate, error)
}

// NormalizeTemplateQuery приводит поисковый запрос к нормализованному виду:
// пробелы по краям сняты, регистр понижен. Нормализация — единая для всех
// реализаций, поэтому поиск одинаков и в памяти, и в PostgreSQL.
func NormalizeTemplateQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

// TemplateMatches сообщает, содержит ли шаблон нормализованный запрос. Поиск
// идёт по имени, теме, телу и объявленным переменным; пустой запрос совпадает со
// всем. Функция общая для реализаций репозитория: она задаёт семантику поиска,
// чтобы in-memory и PostgreSQL не расходились.
func TemplateMatches(template *domain.EmailTemplate, normalizedQuery string) bool {
	if normalizedQuery == "" {
		return true
	}
	return strings.Contains(templateSearchText(template), normalizedQuery)
}

// templateSearchText собирает нормализованный текст шаблона для поиска: имя,
// тему, тело и ключи объявленных переменных.
func templateSearchText(template *domain.EmailTemplate) string {
	var builder strings.Builder
	builder.WriteString(strings.ToLower(template.Name()))
	builder.WriteByte('\n')
	builder.WriteString(strings.ToLower(template.Subject()))
	builder.WriteByte('\n')
	builder.WriteString(strings.ToLower(template.Body()))
	for _, variable := range template.Variables() {
		builder.WriteByte('\n')
		builder.WriteString(strings.ToLower(string(variable.Key)))
	}
	return builder.String()
}

// SortTemplates упорядочивает шаблоны по имени, затем по идентификатору. Порядок
// общий для реализаций: он делает выдачу поиска детерминированной.
func SortTemplates(templates []*domain.EmailTemplate) {
	sort.SliceStable(templates, func(i, j int) bool {
		left, right := strings.ToLower(templates[i].Name()), strings.ToLower(templates[j].Name())
		if left == right {
			return templates[i].ID() < templates[j].ID()
		}
		return left < right
	})
}

// ApplyTemplateLimit обрезает список до предела; limit <= 0 — без предела.
func ApplyTemplateLimit(templates []*domain.EmailTemplate, limit int) []*domain.EmailTemplate {
	if limit > 0 && len(templates) > limit {
		return templates[:limit]
	}
	return templates
}
