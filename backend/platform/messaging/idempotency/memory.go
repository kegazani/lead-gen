package idempotency

import (
	"context"
	"sync"
)

// MemoryStore — потокобезопасный журнал ключей в памяти. Годится для
// совмещённого процесса и тестов; при выделении сервиса заменяется хранилищем
// на стороне владельца без изменения вызовов.
type MemoryStore struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewMemoryStore возвращает пустой журнал ключей.
func NewMemoryStore() Store {
	return &MemoryStore{seen: make(map[string]struct{})}
}

// Seen сообщает, встречался ли ключ. Отменённый контекст — ошибка, а не ложное
// «не видели»: иначе отменённый вызов мог бы выполнить эффект повторно.
func (s *MemoryStore) Seen(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[key]
	return ok, nil
}

// Record фиксирует ключ. Повторная запись того же ключа идемпотентна.
func (s *MemoryStore) Record(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[key] = struct{}{}
	return nil
}
