package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ContactRepository — in-memory реализация ports.ContactRepository.
type ContactRepository struct {
	mu       sync.RWMutex
	contacts map[domain.ContactID]domain.ContactSnapshot
}

// ContactRepository реализует порт репозитория контактов.
var _ ports.ContactRepository = (*ContactRepository)(nil)

// NewContactRepository создаёт пустой репозиторий.
func NewContactRepository() *ContactRepository {
	return &ContactRepository{contacts: make(map[domain.ContactID]domain.ContactSnapshot)}
}

// Save сохраняет контакт целиком как снимок. Повторный вызов для того же
// контакта заменяет прежний снимок (upsert по идентификатору).
func (r *ContactRepository) Save(_ context.Context, contact *domain.Contact) error {
	if contact == nil {
		return errors.New("memory: сохранение nil-контакта")
	}
	snapshot := contact.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contacts[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает контакт по идентификатору через domain.RehydrateContact.
// Отсутствие контакта даёт ports.ErrContactNotFound: владелец различает
// неизвестную ссылку и сбой чтения.
func (r *ContactRepository) FindByID(_ context.Context, id domain.ContactID) (*domain.Contact, error) {
	r.mu.RLock()
	snapshot, ok := r.contacts[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: контакт %s", ports.ErrContactNotFound, id)
	}
	contact, err := domain.RehydrateContact(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление контакта %s: %w", id, err)
	}
	return contact, nil
}

// ListByCompany возвращает контакты компании в порядке постановки
// (CreatedAt, ID). Возвращает только контакты указанной компании.
func (r *ContactRepository) ListByCompany(_ context.Context, companyID domain.CompanyID) ([]*domain.Contact, error) {
	r.mu.RLock()
	snapshots := make([]domain.ContactSnapshot, 0)
	for _, snapshot := range r.contacts {
		if snapshot.CompanyID != companyID {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})

	contacts := make([]*domain.Contact, 0, len(snapshots))
	for _, snapshot := range snapshots {
		contact, err := domain.RehydrateContact(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление контакта %s: %w", snapshot.ID, err)
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}

// ListByCompanies возвращает контакты набора компаний одним проходом по
// хранилищу (без запроса на каждую компанию) в порядке
// (CompanyID, CreatedAt, ID). Пустой набор даёт пустой список.
func (r *ContactRepository) ListByCompanies(_ context.Context, companyIDs []domain.CompanyID) ([]*domain.Contact, error) {
	if len(companyIDs) == 0 {
		return []*domain.Contact{}, nil
	}

	wanted := make(map[domain.CompanyID]struct{}, len(companyIDs))
	for _, companyID := range companyIDs {
		wanted[companyID] = struct{}{}
	}

	r.mu.RLock()
	snapshots := make([]domain.ContactSnapshot, 0)
	for _, snapshot := range r.contacts {
		if _, ok := wanted[snapshot.CompanyID]; !ok {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].CompanyID != snapshots[j].CompanyID {
			return snapshots[i].CompanyID < snapshots[j].CompanyID
		}
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})

	contacts := make([]*domain.Contact, 0, len(snapshots))
	for _, snapshot := range snapshots {
		contact, err := domain.RehydrateContact(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление контакта %s: %w", snapshot.ID, err)
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}
