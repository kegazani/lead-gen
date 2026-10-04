// Package inmemory реализует ports.ProjectionStore поверх оперативной памяти.
//
// Это dev- и тестовая реализация: состояние витрины живёт в памяти процесса и
// теряется при перезапуске. На продуктовом пути хранилище подставляет точка
// сборки (PostgreSQL); этот адаптер остаётся значением по умолчанию только когда
// внешняя СУБД не подключена.
package inmemory

import (
	"context"
	"sort"
	"sync"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// projectionKey — ключ снимка проекции: тенант и компания.
type projectionKey struct {
	tenant  string
	company string
}

// Store — in-memory хранилище снимков проекции.
type Store struct {
	mu        sync.Mutex
	companies map[projectionKey]readmodel.CompanyRecord
}

// Проверка соответствия порту на этапе компиляции.
var _ ports.ProjectionStore = (*Store)(nil)

// New создаёт пустое хранилище проекции.
func New() *Store {
	return &Store{companies: make(map[projectionKey]readmodel.CompanyRecord)}
}

// SaveCompany сохраняет снимок компании. Повторный вызов для того же ключа
// заменяет прежний снимок, не создавая второй записи.
func (s *Store) SaveCompany(_ context.Context, record readmodel.CompanyRecord) error {
	key := projectionKey{tenant: record.TenantID, company: record.CompanyID}
	s.mu.Lock()
	s.companies[key] = record
	s.mu.Unlock()
	return nil
}

// LoadCompanies возвращает сохранённые снимки в устойчивом порядке
// (тенант, компания), чтобы результат не зависел от порядка вставки.
func (s *Store) LoadCompanies(_ context.Context) ([]readmodel.CompanyRecord, error) {
	s.mu.Lock()
	records := make([]readmodel.CompanyRecord, 0, len(s.companies))
	for _, record := range s.companies {
		records = append(records, record)
	}
	s.mu.Unlock()

	sort.Slice(records, func(i, j int) bool {
		if records[i].TenantID != records[j].TenantID {
			return records[i].TenantID < records[j].TenantID
		}
		return records[i].CompanyID < records[j].CompanyID
	})
	return records, nil
}
