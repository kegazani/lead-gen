package inmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// TransactionalJournal — локальный memory journal bounded context. Он реализует
// relay-порт платформы и публикует batch под общим visibility gate с request
// repository; поэтому читатели не наблюдают половину UoW commit.
type TransactionalJournal struct {
	binding atomic.Pointer[journalBinding]
	entries []*transactionalEntry
	index   map[string]*transactionalEntry
}

// journalBinding immutable после bound=true. Atomic replacement позволяет
// связать journal с repository до первого I/O и запрещает смену gate после
// начала операций без mutex/неотменяемого ожидания.
type journalBinding struct {
	gate  *persistence.Gate
	bound bool
}

type transactionalEntry struct {
	message   platformoutbox.Message
	published bool
}

var _ platformoutbox.Journal = (*TransactionalJournal)(nil)

func NewTransactionalJournal() *TransactionalJournal {
	j := &TransactionalJournal{index: make(map[string]*transactionalEntry)}
	j.binding.Store(&journalBinding{gate: persistence.NewGate()})
	return j
}

func (j *TransactionalJournal) VisibilityGate() *persistence.Gate { return j.binding.Load().gate }

// BindVisibilityGate связывает journal с gate repository. Повторное связывание
// допустимо только с тем же хранилищем, иначе два UoW могли бы обойти изоляцию.
func (j *TransactionalJournal) BindVisibilityGate(gate *persistence.Gate) error {
	if gate == nil {
		return errors.New("request-fulfillment: nil repository visibility gate")
	}
	for {
		current := j.binding.Load()
		if current.bound {
			if current.gate == gate {
				return nil
			} // identical rebind не записывает pointer
			return ErrJournalBoundToDifferentGate
		}
		next := &journalBinding{gate: gate, bound: true}
		if j.binding.CompareAndSwap(current, next) {
			return nil
		}
	}
}

var ErrJournalBoundToDifferentGate = errors.New("request-fulfillment: journal already bound to another repository gate")

func (j *TransactionalJournal) gateForOperation() *persistence.Gate {
	for {
		current := j.binding.Load()
		if current.bound {
			return current.gate
		}
		sealed := &journalBinding{gate: current.gate, bound: true}
		if j.binding.CompareAndSwap(current, sealed) {
			return sealed.gate
		}
	}
}

// CommitBatch атомарно применяет outbox batch и repository commit. Валидация и
// подготовка всех сообщений происходят до вызова commit; после него остаются
// только неошибающие присваивания состояния под тем же gate.
func (j *TransactionalJournal) CommitBatch(ctx context.Context, messages []platformoutbox.Message, commit func()) error {
	gate := j.gateForOperation()
	if err := gate.Acquire(ctx); err != nil {
		return err
	}
	defer gate.Release()
	return j.CommitBatchUnderGate(ctx, messages, commit)
}

// CommitBatchUnderGate атомарно коммитит batch и callback под уже захваченным
// общим gate; повторно брать gate запрещено во избежание deadlock.
func (j *TransactionalJournal) CommitBatchUnderGate(ctx context.Context, messages []platformoutbox.Message, commit func()) error {
	if commit == nil {
		return errors.New("request-fulfillment: отсутствует UoW commit")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, message := range messages {
		if strings.TrimSpace(message.ID) == "" {
			return errors.New("request-fulfillment: outbox message без ID")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries := append([]*transactionalEntry(nil), j.entries...)
	index := make(map[string]*transactionalEntry, len(j.index)+len(messages))
	for id, entry := range j.index {
		index[id] = entry
	}
	for _, message := range messages {
		if _, exists := index[message.ID]; exists {
			continue
		}
		entry := &transactionalEntry{message: cloneOutboxMessage(message)}
		entries = append(entries, entry)
		index[message.ID] = entry
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	commit()
	j.entries = entries
	j.index = index
	return nil
}

func (j *TransactionalJournal) Append(ctx context.Context, message platformoutbox.Message) error {
	return j.CommitBatch(ctx, []platformoutbox.Message{message}, func() {})
}

func (j *TransactionalJournal) Pending(ctx context.Context, limit int) ([]platformoutbox.Message, error) {
	gate := j.gateForOperation()
	if err := gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer gate.Release()
	result := make([]platformoutbox.Message, 0)
	for _, entry := range j.entries {
		if entry.published {
			continue
		}
		if limit > 0 && len(result) >= limit {
			break
		}
		result = append(result, cloneOutboxMessage(entry.message))
	}
	return result, nil
}

func (j *TransactionalJournal) MarkPublished(ctx context.Context, id string) error {
	gate := j.gateForOperation()
	if err := gate.Acquire(ctx); err != nil {
		return err
	}
	defer gate.Release()
	entry, ok := j.index[id]
	if !ok {
		return fmt.Errorf("request-fulfillment outbox: неизвестная запись %q", id)
	}
	entry.published = true
	return nil
}

func cloneOutboxMessage(m platformoutbox.Message) platformoutbox.Message {
	copy := platformoutbox.Message{ID: m.ID, Exchange: m.Exchange, RoutingKey: m.RoutingKey, Body: append([]byte(nil), m.Body...)}
	if m.Headers != nil {
		copy.Headers = make(map[string]string, len(m.Headers))
		for key, value := range m.Headers {
			copy.Headers[key] = value
		}
	}
	return copy
}
