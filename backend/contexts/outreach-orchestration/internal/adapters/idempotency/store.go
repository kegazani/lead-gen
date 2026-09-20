// Package idempotency реализует хранилище ключей идемпотентности команд на
// границе контекста outreach-orchestration.
//
// Ключ команды — пара (tenant_id, command_id) контракта campaign-command.
// Хранилище резервирует ключ до выполнения эффекта (Reserve), а результат
// (идентификатор кампании) публикует после него (Complete). Поэтому при гонке
// двух одинаковых команд эффект выполняет только владелец резервирования, а
// проигравший получает уже существующий результат — второй кампании не
// возникает (design.md, Решение 5; задача 6.2). Хранилище хранит только
// идентификаторы и не знает о прикладном слое, портах или типах внешних систем.
package idempotency

import (
	"context"
	"errors"
	"sync"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Key — ключ идемпотентности команды: граница тенанта и идентификатор команды.
type Key struct {
	TenantID  domain.TenantID
	CommandID domain.CommandID
}

// ErrReservationAbandoned — владелец резервирования отказался от него, не
// записав результат. Ожидающие повтора получают эту ошибку и вправе повторить
// команду позже: ключ освобождён.
var ErrReservationAbandoned = errors.New("idempotency: резервирование команды отменено")

// entry — состояние одного ключа: резервирование в процессе или готовый
// результат. ready закрывается ровно один раз при завершении (Complete или
// Release); до этого ожидающие блокируются на нём.
type entry struct {
	campaignID domain.CampaignID
	done       bool
	err        error
	ready      chan struct{}
}

// Store — in-memory хранилище соответствия ключа команды идентификатору
// кампании. Безопасно для конкурентного использования.
//
// Ключ резервируется атомарно: Reserve выигрывает ровно один вызывающий, а
// остальные ждут его результата. Пик в сторадже возможен только на время
// выполнения эффекта владельцем, поэтому проигравшие не запускают эффект
// повторно.
type Store struct {
	mu      sync.Mutex
	entries map[Key]*entry
}

// NewStore создаёт пустое хранилище ключей.
func NewStore() *Store { return &Store{entries: make(map[Key]*entry)} }

// Reserve резервирует ключ команды за кандидатом.
//
// Если ключ свободен, возвращает (кандидат, true, nil): вызывающий обязан
// выполнить эффект и вызвать Complete (или Release при ошибке). Если ключ уже
// занят или завершён, возвращает результат владельца и false; при незавершённом
// резервировании ожидает его завершения или отмены контекста.
//
// Резервирование до эффекта закрывает TOCTOU: даже одинаковые команды с разными
// campaign_id дают одну кампанию — эффект выполняет только владелец.
func (s *Store) Reserve(
	ctx context.Context,
	tenantID domain.TenantID,
	commandID domain.CommandID,
	candidate domain.CampaignID,
) (domain.CampaignID, bool, error) {
	key := Key{TenantID: tenantID, CommandID: commandID}

	s.mu.Lock()
	current, ok := s.entries[key]
	if !ok {
		reserved := &entry{campaignID: candidate, ready: make(chan struct{})}
		s.entries[key] = reserved
		s.mu.Unlock()
		return candidate, true, nil
	}
	s.mu.Unlock()

	select {
	case <-current.ready:
		s.mu.Lock()
		campaignID, err := current.campaignID, current.err
		s.mu.Unlock()
		if err != nil {
			return "", false, err
		}
		return campaignID, false, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

// Complete публикует результат владельца резервирования и будит ожидающих.
// Повторный вызов для завершённого ключа игнорируется.
func (s *Store) Complete(tenantID domain.TenantID, commandID domain.CommandID, campaignID domain.CampaignID) {
	key := Key{TenantID: tenantID, CommandID: commandID}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.entries[key]
	if !ok || current.done {
		return
	}
	current.campaignID = campaignID
	current.done = true
	close(current.ready)
}

// Release отменяет незавершённое резервирование и освобождает ключ, чтобы
// повтор команды мог зарезервировать его заново. Ожидающие получают
// ErrReservationAbandoned. Завершённый ключ Release не трогает.
func (s *Store) Release(tenantID domain.TenantID, commandID domain.CommandID) {
	key := Key{TenantID: tenantID, CommandID: commandID}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.entries[key]
	if !ok || current.done {
		return
	}
	current.err = ErrReservationAbandoned
	current.done = true
	close(current.ready)
	delete(s.entries, key)
}

// Len возвращает число записанных ключей. Метод предназначен для проверок и
// диагностики, на обработку команд не влияет.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
