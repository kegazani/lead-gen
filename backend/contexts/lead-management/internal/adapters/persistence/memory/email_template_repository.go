package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// EmailTemplateRepository — in-memory реализация ports.EmailTemplateRepository.
type EmailTemplateRepository struct {
	mu        sync.RWMutex
	templates map[domain.TemplateID]domain.EmailTemplateSnapshot
}

// EmailTemplateRepository реализует порт репозитория шаблонов письма.
var _ ports.EmailTemplateRepository = (*EmailTemplateRepository)(nil)

// NewEmailTemplateRepository создаёт пустой репозиторий.
func NewEmailTemplateRepository() *EmailTemplateRepository {
	return &EmailTemplateRepository{templates: make(map[domain.TemplateID]domain.EmailTemplateSnapshot)}
}

// Save сохраняет шаблон целиком как снимок (upsert по идентификатору).
func (r *EmailTemplateRepository) Save(_ context.Context, template *domain.EmailTemplate) error {
	if template == nil {
		return errors.New("memory: сохранение nil-шаблона")
	}
	snapshot := template.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает шаблон по идентификатору через
// domain.RehydrateEmailTemplate. Отсутствие даёт ports.ErrTemplateNotFound.
func (r *EmailTemplateRepository) FindByID(_ context.Context, id domain.TemplateID) (*domain.EmailTemplate, error) {
	r.mu.RLock()
	snapshot, ok := r.templates[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrTemplateNotFound, id)
	}
	template, err := domain.RehydrateEmailTemplate(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление шаблона %s: %w", id, err)
	}
	return template, nil
}

// Delete удаляет шаблон по идентификатору. Отсутствие даёт
// ports.ErrTemplateNotFound.
func (r *EmailTemplateRepository) Delete(_ context.Context, id domain.TemplateID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.templates[id]; !ok {
		return fmt.Errorf("%w: %s", ports.ErrTemplateNotFound, id)
	}
	delete(r.templates, id)
	return nil
}

// Search ищет шаблоны по нормализованному запросу (имя, тема, тело, объявленные
// переменные). Семантика поиска общая с PostgreSQL и задаётся ports.TemplateMatches.
func (r *EmailTemplateRepository) Search(_ context.Context, query string, limit int) ([]*domain.EmailTemplate, error) {
	normalized := ports.NormalizeTemplateQuery(query)

	r.mu.RLock()
	snapshots := make([]domain.EmailTemplateSnapshot, 0, len(r.templates))
	for _, snapshot := range r.templates {
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	templates := make([]*domain.EmailTemplate, 0, len(snapshots))
	for _, snapshot := range snapshots {
		template, err := domain.RehydrateEmailTemplate(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление шаблона %s: %w", snapshot.ID, err)
		}
		if !ports.TemplateMatches(template, normalized) {
			continue
		}
		templates = append(templates, template)
	}
	ports.SortTemplates(templates)
	return ports.ApplyTemplateLimit(templates, limit), nil
}
