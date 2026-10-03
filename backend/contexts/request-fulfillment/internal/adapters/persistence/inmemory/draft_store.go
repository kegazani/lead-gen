package inmemory

import (
	"context"
	"sync"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// DraftStore — in-memory реализация ports.EmailDraftStore: хранит черновики писем
// оператора, сгруппированные по заявке. Состояние защищено мьютексом, поэтому
// хранилище пригодно для `go test -race` и параллельных читателей.
//
// Хранилище намеренно минимально: черновик — производный материал шага
// фулфилмента, а не часть агрегата заявки. Повторное сохранение по тому же лиду
// заменяет прежний черновик, сохраняя место в порядке.
type DraftStore struct {
	mu    sync.RWMutex
	byReq map[string][]ports.EmailDraft
}

// DraftStore реализует порт хранилища черновиков.
var _ ports.EmailDraftStore = (*DraftStore)(nil)

// NewDraftStore создаёт пустое хранилище черновиков.
func NewDraftStore() *DraftStore {
	return &DraftStore{byReq: make(map[string][]ports.EmailDraft)}
}

// SaveDrafts сохраняет черновики, добавляя их к уже имеющимся. Черновик с
// существующим лидом заявки заменяется на месте.
func (s *DraftStore) SaveDrafts(_ context.Context, drafts []ports.EmailDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, draft := range drafts {
		existing := s.byReq[draft.RequestID]
		replaced := false
		for i := range existing {
			if existing[i].LeadID == draft.LeadID {
				existing[i] = draft
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, draft)
		}
		s.byReq[draft.RequestID] = existing
	}
	return nil
}

// ListByRequest возвращает черновики заявки, принадлежащие тенанту, в порядке
// найденных лидов. Возвращается копия: вызывающий не может изменить хранилище.
func (s *DraftStore) ListByRequest(_ context.Context, requestID, tenantID string) ([]ports.EmailDraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored := s.byReq[requestID]
	result := make([]ports.EmailDraft, 0, len(stored))
	for _, draft := range stored {
		if draft.TenantID != tenantID {
			continue
		}
		result = append(result, draft)
	}
	return result, nil
}
