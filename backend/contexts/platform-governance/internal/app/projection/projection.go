// Package projection содержит операционные проекции портала.
//
// Проекция «тенант → последняя поданная заявка» — не агрегат и не хранилище
// заявок: владелец заявки — request-fulfillment, а портал лишь помнит
// идентификатор, чтобы показать клиенту состояние его заявки. Проекция
// переживает процесс только в памяти и на владение моделью не претендует.
package projection

import (
	"sync"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// LastRequest — потокобезопасная проекция «тенант → последняя поданная заявка».
type LastRequest struct {
	mu   sync.RWMutex
	last map[domain.TenantID]string
}

// NewLastRequest создаёт пустую проекцию.
func NewLastRequest() *LastRequest {
	return &LastRequest{last: make(map[domain.TenantID]string)}
}

// Remember запоминает заявку тенанта. Пустые значения игнорируются.
func (p *LastRequest) Remember(tenant domain.TenantID, requestID string) {
	if p == nil || tenant == "" || requestID == "" {
		return
	}
	p.mu.Lock()
	p.last[tenant] = requestID
	p.mu.Unlock()
}

// Get возвращает последнюю поданную заявку тенанта и признак её наличия.
func (p *LastRequest) Get(tenant domain.TenantID) (string, bool) {
	if p == nil {
		return "", false
	}
	p.mu.RLock()
	requestID, ok := p.last[tenant]
	p.mu.RUnlock()
	return requestID, ok
}
