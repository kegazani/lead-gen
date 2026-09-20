package outbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// journalEntry — запись журнала с признаком публикации.
type journalEntry struct {
	msg       Message
	published bool
}

// MemoryJournal — потокобезопасный журнал outbox в памяти.
//
// Годится для совмещённого процесса и тестов; при выделении сервиса заменяется
// таблицей outbox владельца без изменения вызовов. Append идемпотентен по ID:
// повторная запись той же записи не создаёт вторую (владелец может повторить
// транзакцию). MarkPublished помечает запись опубликованной, поэтому после
// рестарта relay не выдаёт её снова.
type MemoryJournal struct {
	mu      sync.Mutex
	entries []*journalEntry
	index   map[string]*journalEntry
}

// NewMemoryJournal возвращает пустой журнал.
func NewMemoryJournal() *MemoryJournal {
	return &MemoryJournal{index: make(map[string]*journalEntry)}
}

// Append добавляет запись в порядке вызова. Запись без ID — ошибка: ID служит
// ключом идемпотентности публикации. Повторная запись с тем же ID — no-op.
func (j *MemoryJournal) Append(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("outbox: запись без ID")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.index[m.ID]; ok {
		return nil
	}
	e := &journalEntry{msg: cloneMessage(m)}
	j.entries = append(j.entries, e)
	j.index[m.ID] = e
	return nil
}

// Pending возвращает ещё не опубликованные записи в порядке записи. limit больше
// нуля ограничивает размер порции; limit <= 0 возвращает все ожидающие.
func (j *MemoryJournal) Pending(ctx context.Context, limit int) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	out := make([]Message, 0)
	for _, e := range j.entries {
		if e.published {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, cloneMessage(e.msg))
	}
	return out, nil
}

// MarkPublished подтверждает успешную публикацию записи. Неизвестный ID —
// ошибка: подтверждать нечего, и молчаливый no-op скрыл бы рассинхронизацию.
func (j *MemoryJournal) MarkPublished(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.index[id]
	if !ok {
		return fmt.Errorf("outbox: подтверждение неизвестной записи %q", id)
	}
	e.published = true
	return nil
}

// cloneMessage копирует запись, чтобы журнал не делил буферы с вызывающим.
func cloneMessage(m Message) Message {
	return Message{
		ID:         m.ID,
		Exchange:   m.Exchange,
		RoutingKey: m.RoutingKey,
		Body:       cloneBytes(m.Body),
		Headers:    cloneHeaders(m.Headers),
	}
}

// cloneBytes копирует срез байт, сохраняя nil.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// cloneHeaders копирует карту заголовков, сохраняя nil.
func cloneHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}
