package inmemory

import (
	"context"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// InsightStore — in-memory реализация ports.InsightStore (dev и тесты): снимки
// качества поиска и применения рекомендаций живут в памяти процесса и теряются
// при перезапуске. Хранилище не содержит персональных данных: только исходы с
// непрозрачными ссылками на компании.
type InsightStore struct {
	mu           sync.Mutex
	snapshots    map[string]readmodel.InsightSnapshot
	applications map[applicationKey]readmodel.RecommendationApplication
}

// applicationKey — ключ идемпотентности применения рекомендации.
type applicationKey struct {
	tenant    string
	scopeKind string
	scopeKey  string
	appliedAt time.Time
}

// InsightStore реализует порт на этапе компиляции.
var _ ports.InsightStore = (*InsightStore)(nil)

// NewInsightStore создаёт пустое хранилище инсайтов.
func NewInsightStore() *InsightStore {
	return &InsightStore{
		snapshots:    make(map[string]readmodel.InsightSnapshot),
		applications: make(map[applicationKey]readmodel.RecommendationApplication),
	}
}

// SaveSnapshot сохраняет снимок тенанта, заменяя прежний (upsert по тенанту).
func (s *InsightStore) SaveSnapshot(_ context.Context, snapshot readmodel.InsightSnapshot) error {
	s.mu.Lock()
	s.snapshots[snapshot.TenantID] = snapshot
	s.mu.Unlock()
	return nil
}

// LoadSnapshots возвращает снимки в устойчивом порядке (тенант).
func (s *InsightStore) LoadSnapshots(_ context.Context) ([]readmodel.InsightSnapshot, error) {
	s.mu.Lock()
	snapshots := make([]readmodel.InsightSnapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		snapshots = append(snapshots, snapshot)
	}
	s.mu.Unlock()

	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].TenantID < snapshots[j].TenantID })
	return snapshots, nil
}

// SaveApplication фиксирует применение рекомендации, заменяя запись по ключу.
func (s *InsightStore) SaveApplication(_ context.Context, application readmodel.RecommendationApplication) error {
	key := applicationKey{
		tenant:    application.TenantID,
		scopeKind: application.ScopeKind,
		scopeKey:  application.ScopeKey,
		appliedAt: application.AppliedAt,
	}
	s.mu.Lock()
	s.applications[key] = application
	s.mu.Unlock()
	return nil
}

// LoadApplications возвращает применения в устойчивом порядке (тенант, срез,
// время).
func (s *InsightStore) LoadApplications(_ context.Context) ([]readmodel.RecommendationApplication, error) {
	s.mu.Lock()
	applications := make([]readmodel.RecommendationApplication, 0, len(s.applications))
	for _, application := range s.applications {
		applications = append(applications, application)
	}
	s.mu.Unlock()

	sort.Slice(applications, func(i, j int) bool {
		left, right := applications[i], applications[j]
		if left.TenantID != right.TenantID {
			return left.TenantID < right.TenantID
		}
		if left.ScopeKind != right.ScopeKind {
			return left.ScopeKind < right.ScopeKind
		}
		if left.ScopeKey != right.ScopeKey {
			return left.ScopeKey < right.ScopeKey
		}
		return left.AppliedAt.Before(right.AppliedAt)
	})
	return applications, nil
}
