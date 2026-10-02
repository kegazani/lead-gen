// Package inmemory реализует репозитории message и sender-reputation поверх
// оперативной памяти.
//
// Адаптеры хранят не указатели на агрегаты, а снимки состояния, а загрузка идёт
// через domain.Rehydrate*. Поэтому сохранённый агрегат изолирован от
// вызывающего: последующие правки не меняют уже сохранённое состояние, а
// загрузка заново проверяет инварианты. Пакет не знает о транзакциях, драйверах
// и SQL — атомарностью записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Messages — in-memory реализация ports.MessageRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и для параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Messages struct {
	mu    sync.RWMutex
	byID  map[domain.MessageID]domain.MessageSnapshot
	byKey map[string]domain.MessageID
}

// Messages реализует порт репозитория сообщений.
var _ ports.MessageRepository = (*Messages)(nil)

// NewMessages создаёт пустой репозиторий сообщений.
func NewMessages() *Messages {
	return &Messages{
		byID:  make(map[domain.MessageID]domain.MessageSnapshot),
		byKey: make(map[string]domain.MessageID),
	}
}

// Save сохраняет сообщение целиком как снимок. Повторный вызов для того же
// идентификатора заменяет прежний снимок (upsert), не создавая второе
// сообщение. Ключ идемпотентности индексируется: повторная команда с тем же
// ключом найдёт существующее сообщение и не создаст второе.
func (r *Messages) Save(_ context.Context, message *domain.Message) error {
	if message == nil {
		return errors.New("inmemory: сохранение nil-сообщения")
	}
	snapshot := message.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	if existingID, ok := r.byKey[snapshot.IdempotencyKey]; ok && existingID != snapshot.ID {
		return fmt.Errorf("inmemory: ключ идемпотентности %q занят сообщением %s",
			snapshot.IdempotencyKey, existingID)
	}
	r.byID[snapshot.ID] = snapshot
	r.byKey[snapshot.IdempotencyKey] = snapshot.ID
	return nil
}

// FindByID загружает сообщение по идентификатору через domain.RehydrateMessage.
func (r *Messages) FindByID(_ context.Context, id domain.MessageID) (*domain.Message, error) {
	r.mu.RLock()
	snapshot, ok := r.byID[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrMessageNotFound, id)
	}
	message, err := domain.RehydrateMessage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление сообщения %s: %w", id, err)
	}
	return message, nil
}

// FindByIdempotencyKey загружает сообщение по ключу идемпотентности.
func (r *Messages) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Message, error) {
	r.mu.RLock()
	id, ok := r.byKey[key]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: ключ %q", ports.ErrMessageNotFound, key)
	}
	return r.FindByID(ctx, id)
}

// Clone возвращает независимую копию репозитория для одной транзакции.
func (r *Messages) Clone() *Messages {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := NewMessages()
	for id, snapshot := range r.byID {
		clone.byID[id] = snapshot
	}
	for key, id := range r.byKey {
		clone.byKey[key] = id
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit: рабочая копия транзакции становится основным
// состоянием. При откате ReplaceWith не вызывается, и копия отбрасывается.
func (r *Messages) ReplaceWith(other *Messages) {
	other.mu.RLock()
	byID := make(map[domain.MessageID]domain.MessageSnapshot, len(other.byID))
	for id, snapshot := range other.byID {
		byID[id] = snapshot
	}
	byKey := make(map[string]domain.MessageID, len(other.byKey))
	for key, id := range other.byKey {
		byKey[key] = id
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.byID = byID
	r.byKey = byKey
	r.mu.Unlock()
}
