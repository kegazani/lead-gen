// Package inmemory реализует порты хранилища контекста engagement-observation
// поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.EngagementSnapshot), а загрузка идёт через domain.Rehydrate. Поэтому
// сохранённый агрегат изолирован от вызывающего, а загрузка заново проверяет
// инварианты. Методы Clone и ReplaceWith существуют для unit-of-work: транзакция
// работает с копией, а commit переносит её состояние.
//
// Пакет не знает о транзакциях, драйверах и SQL — атомарностью записи управляет
// unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// Repository — in-memory реализация ports.EngagementRepository.
type Repository struct {
	mu     sync.RWMutex
	events map[domain.EngagementEventID]domain.EngagementSnapshot
}

// Repository реализует порт хранилища событий взаимодействия.
var _ ports.EngagementRepository = (*Repository)(nil)

// New создаёт пустое хранилище.
func New() *Repository {
	return &Repository{events: make(map[domain.EngagementEventID]domain.EngagementSnapshot)}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
// При создании (отсутствующем в хранилище) expectedVersion равен нулю; повторное
// сохранение заменяет прежний снимок при совпадении версии.
func (r *Repository) Save(_ context.Context, event *domain.EngagementEvent, expectedVersion int) error {
	if event == nil {
		return errors.New("inmemory: сохранение nil-события")
	}
	snapshot := event.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()

	if stored, ok := r.events[snapshot.ID]; ok {
		if stored.Version != expectedVersion {
			return fmt.Errorf(
				"%w: событие %s: сохранено %d, ожидалось %d",
				ports.ErrVersionConflict, snapshot.ID, stored.Version, expectedVersion,
			)
		}
	} else if expectedVersion != 0 {
		return fmt.Errorf(
			"%w: событие %s: нет сохранённой версии, ожидалось %d",
			ports.ErrVersionConflict, snapshot.ID, expectedVersion,
		)
	}

	r.events[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает агрегат по идентификатору через domain.Rehydrate.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err, ports.ErrEventNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.EngagementEventID) (*domain.EngagementEvent, error) {
	r.mu.RLock()
	snapshot, ok := r.events[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrEventNotFound, id)
	}
	event, err := domain.Rehydrate(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление события %s: %w", id, err)
	}
	return event, nil
}

// ListByTenant возвращает события тенанта, упорядоченные по идентификатору.
func (r *Repository) ListByTenant(_ context.Context, tenant domain.TenantID) ([]*domain.EngagementEvent, error) {
	r.mu.RLock()
	ids := make([]domain.EngagementEventID, 0, len(r.events))
	for id, snapshot := range r.events {
		if snapshot.TenantID == tenant {
			ids = append(ids, id)
		}
	}
	r.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	events := make([]*domain.EngagementEvent, 0, len(ids))
	for _, id := range ids {
		event, err := r.FindByID(context.Background(), id)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// Clone возвращает независимую копию хранилища для одной транзакции.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := New()
	for id, snapshot := range r.events {
		clone.events[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние хранилища состоянием другого. Вызывается
// unit-of-work в момент commit; при откате не вызывается, и копия отбрасывается.
func (r *Repository) ReplaceWith(other *Repository) {
	other.mu.RLock()
	snapshots := make(map[domain.EngagementEventID]domain.EngagementSnapshot, len(other.events))
	for id, snapshot := range other.events {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.events = snapshots
	r.mu.Unlock()
}

// Ledger — in-memory реализация ports.SignalLedger: журнал обработанных сырых
// сигналов для дедупликации входящих webhook.
type Ledger struct {
	mu   sync.RWMutex
	seen map[domain.SignalID]struct{}
}

// Ledger реализует порт журнала дедупликации сигналов.
var _ ports.SignalLedger = (*Ledger)(nil)

// NewLedger создаёт пустой журнал.
func NewLedger() *Ledger {
	return &Ledger{seen: make(map[domain.SignalID]struct{})}
}

// Seen сообщает, обрабатывался ли сигнал ранее.
func (l *Ledger) Seen(_ context.Context, key domain.SignalID) (bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.seen[key]
	return ok, nil
}

// Mark фиксирует сигнал как обработанный. Пустой ключ отвергается: дедуплицировать
// нечем.
func (l *Ledger) Mark(_ context.Context, key domain.SignalID) error {
	if strings.TrimSpace(string(key)) == "" {
		return errors.New("inmemory: отметка пустого ключа дедупликации")
	}
	l.mu.Lock()
	l.seen[key] = struct{}{}
	l.mu.Unlock()
	return nil
}

// Clone возвращает независимую копию журнала для одной транзакции.
func (l *Ledger) Clone() *Ledger {
	l.mu.RLock()
	defer l.mu.RUnlock()
	clone := NewLedger()
	for key := range l.seen {
		clone.seen[key] = struct{}{}
	}
	return clone
}

// ReplaceWith заменяет состояние журнала состоянием другого при commit.
func (l *Ledger) ReplaceWith(other *Ledger) {
	other.mu.RLock()
	seen := make(map[domain.SignalID]struct{}, len(other.seen))
	for key := range other.seen {
		seen[key] = struct{}{}
	}
	other.mu.RUnlock()

	l.mu.Lock()
	l.seen = seen
	l.mu.Unlock()
}

// Links — in-memory реализация ports.TransitionLinks: реестр трекинг-ссылок
// capability read-tracking.
type Links struct {
	mu    sync.RWMutex
	links map[domain.LinkRef]domain.TransitionLink
}

// Links реализует порт реестра трекинг-ссылок.
var _ ports.TransitionLinks = (*Links)(nil)

// NewLinks создаёт пустой реестр.
func NewLinks() *Links {
	return &Links{links: make(map[domain.LinkRef]domain.TransitionLink)}
}

// Save регистрирует ссылку перехода. Повторная регистрация той же ссылки
// заменяет прежний адрес: владелец ссылки вправе её переопределить.
func (l *Links) Save(_ context.Context, link domain.TransitionLink) error {
	if strings.TrimSpace(string(link.Ref)) == "" {
		return errors.New("inmemory: регистрация ссылки без идентификатора")
	}
	l.mu.Lock()
	l.links[link.Ref] = link
	l.mu.Unlock()
	return nil
}

// Resolve возвращает ссылку перехода по непрозрачному значению. Отсутствие даёт
// ошибку, для которой истинно errors.Is(err, ports.ErrLinkNotFound).
func (l *Links) Resolve(_ context.Context, ref domain.LinkRef) (domain.TransitionLink, error) {
	l.mu.RLock()
	link, ok := l.links[ref]
	l.mu.RUnlock()
	if !ok {
		return domain.TransitionLink{}, fmt.Errorf("%w: %s", ports.ErrLinkNotFound, ref)
	}
	return link, nil
}

// Clone возвращает независимую копию реестра для одной транзакции.
func (l *Links) Clone() *Links {
	l.mu.RLock()
	defer l.mu.RUnlock()
	clone := NewLinks()
	for ref, link := range l.links {
		clone.links[ref] = link
	}
	return clone
}

// ReplaceWith заменяет состояние реестра состоянием другого при commit.
func (l *Links) ReplaceWith(other *Links) {
	other.mu.RLock()
	links := make(map[domain.LinkRef]domain.TransitionLink, len(other.links))
	for ref, link := range other.links {
		links[ref] = link
	}
	other.mu.RUnlock()

	l.mu.Lock()
	l.links = links
	l.mu.Unlock()
}
