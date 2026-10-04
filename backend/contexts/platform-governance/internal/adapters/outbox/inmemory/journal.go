// Package inmemory реализует transactional outbox и unit-of-work поверх
// оперативной памяти: журнал записей, привязанный к транзакции, и сама
// транзакция для тестов и локального запуска.
//
// Сообщения добавляются в outbox активной транзакции и попадают в журнал только
// после успешного commit. Откат транзакции не оставляет ни агрегата, ни
// сообщений, ни ключа идемпотентности. Публикацию подтверждённых записей
// выполняет отдельный механизм (relay/worker); пакет не знает о брокере и
// внешних системах.
package inmemory

import (
	"context"
	"strconv"
	"sync"

	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// Journal — журнал outbox в памяти: записи, ожидающие публикации, и признак
// подтверждения. Журнал разделяется unit-of-work (пишет при commit) и
// потребителем публикации (читает и подтверждает), поэтому защищён мьютексом.
type Journal struct {
	mu      sync.Mutex
	records []journalRecord
	seq     uint64
}

// Entry — запись журнала, ожидающая публикации.
type Entry struct {
	// ID — идентификатор записи журнала (не контракта).
	ID string
	// Message — публикуемое сообщение контракта.
	Message ports.Message
}

type journalRecord struct {
	id        string
	message   ports.Message
	published bool
}

// NewJournal создаёт пустой журнал outbox.
func NewJournal() *Journal { return &Journal{} }

// appendMessages добавляет сообщения в журнал. Вызывается unit-of-work только
// после успешного commit, поэтому журнал не содержит записей откатанных
// транзакций.
func (j *Journal) appendMessages(messages ...ports.Message) {
	if len(messages) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, message := range messages {
		j.seq++
		j.records = append(j.records, journalRecord{
			id:      message.Contract + "/" + strconv.FormatUint(j.seq, 10),
			message: message,
		})
	}
}

// Pending возвращает неподтверждённые записи в порядке добавления.
func (j *Journal) Pending(_ context.Context) ([]Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var pending []Entry
	for _, record := range j.records {
		if record.published {
			continue
		}
		pending = append(pending, Entry{ID: record.id, Message: record.message})
	}
	return pending, nil
}

// MarkPublished подтверждает публикацию записей по их идентификаторам.
// Неизвестные идентификаторы игнорируются: подтверждение идемпотентно.
func (j *Journal) MarkPublished(_ context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	toMark := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		toMark[id] = struct{}{}
	}
	for i := range j.records {
		if _, ok := toMark[j.records[i].id]; ok {
			j.records[i].published = true
		}
	}
	return nil
}

// PendingCount возвращает число неподтверждённых записей. Вспомогательный метод
// для проверок: контракт публикации его не требует.
func (j *Journal) PendingCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, record := range j.records {
		if !record.published {
			n++
		}
	}
	return n
}
