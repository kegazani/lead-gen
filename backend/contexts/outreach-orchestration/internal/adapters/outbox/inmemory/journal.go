// Package inmemory реализует transactional outbox поверх оперативной памяти:
// журнал записей, привязанный к транзакции, и сам unit-of-work для тестов и
// локального запуска.
//
// События добавляются в outbox активной транзакции и попадают в журнал только
// после успешного commit. Откат транзакции не оставляет ни агрегата, ни
// событий. Публикацию подтверждённых записей выполняет relay, читающий журнал;
// пакет не знает о брокере и внешних системах.
package inmemory

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Journal — журнал outbox в памяти: записи, ожидающие публикации, и признак
// подтверждения. Журнал разделяется unit-of-work (пишет при commit) и relay
// (читает и подтверждает), поэтому защищён мьютексом.
type Journal struct {
	mu      sync.Mutex
	records []journalRecord
	seq     uint64
}

type journalRecord struct {
	id        string
	event     domain.DomainEvent
	published bool
}

// Journal реализует источник публикации для relay.
var _ relay.Source = (*Journal)(nil)

// NewJournal создаёт пустой журнал outbox.
func NewJournal() *Journal { return &Journal{} }

// appendEvents добавляет события в журнал. Вызывается unit-of-work только
// после успешного commit, поэтому журнал не содержит записей откатанных
// транзакций.
func (j *Journal) appendEvents(events ...domain.DomainEvent) {
	if len(events) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, event := range events {
		j.seq++
		j.records = append(j.records, journalRecord{
			id:    fmt.Sprintf("outbox-%d", j.seq),
			event: event,
		})
	}
}

// Pending возвращает неподтверждённые записи в порядке добавления.
func (j *Journal) Pending(_ context.Context) ([]relay.Message, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var pending []relay.Message
	for _, record := range j.records {
		if record.published {
			continue
		}
		pending = append(pending, relay.Message{ID: record.id, Event: record.event})
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

// PendingCount возвращает число неподтверждённых записей. Вспомогательный
// метод для проверок: сам контракт публикации его не требует.
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
