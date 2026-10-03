package consoleauth

import (
	"context"
	"sync"
	"time"
)

// Record — сохранённая серверная сессия оператора. Это плоское представление
// сессии в хранилище: его читает и пишет любой Store, включая общий стор консоли
// (PostgreSQL), поэтому запись не ссылается на изменяемую структуру Session.
type Record struct {
	// ID — идентификатор сессии (значение cookie).
	ID string
	// Username — логин оператора, которому выдана сессия.
	Username string
	// CSRF — CSRF-токен сессии.
	CSRF string
	// LastActive — момент последней активности; по нему проверяется простой.
	LastActive time.Time
}

// Store хранит серверные сессии оператора. Реализации обязаны быть
// потокобезопасными: одну запись могут одновременно читать и писать несколько
// HTTP-обработчиков, а общий стор — ещё и разные процессы консоли.
//
// Load возвращает ok=false, если сессии с таким ID нет. Отсутствие записи — не
// ошибка: так различаются «сессии нет» и «хранилище недоступно».
type Store interface {
	Save(ctx context.Context, rec Record) error
	Load(ctx context.Context, id string) (Record, bool, error)
	Delete(ctx context.Context, id string) error
}

// MemoryStore — стор сессий в памяти процесса. Он же значение по умолчанию:
// без общего стора консоль работает как прежде — сессии живут в памяти. Годится
// и для доказательства общего входа: два сервера с одним MemoryStore видят
// сессии друг друга.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]Record
}

// NewMemoryStore создаёт пустой стор сессий в памяти.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]Record)}
}

// Save записывает сессию, заменяя прежнюю запись с тем же ID.
func (s *MemoryStore) Save(_ context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]Record)
	}
	s.sessions[rec.ID] = rec
	return nil
}

// Load возвращает сессию по ID; ok=false, если её нет.
func (s *MemoryStore) Load(_ context.Context, id string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

// Delete удаляет сессию. Операция идемпотентна.
func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ Store = (*MemoryStore)(nil)
