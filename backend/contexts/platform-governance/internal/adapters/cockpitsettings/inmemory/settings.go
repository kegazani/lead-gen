// Package inmemory — настройки экрана операций в памяти процесса. Подходит
// одному экземпляру роли api (dev и линейная сборка): настройки интерфейса не
// переживают перезапуск и не делятся между процессами, но и не выдаются за
// постоянное хранилище.
package inmemory

import (
	"context"
	"strings"
	"sync"
)

// Settings хранит множество скрытых панелей по субъекту.
type Settings struct {
	mu     sync.RWMutex
	hidden map[string]map[string]bool
}

// New собирает пустое хранилище настроек.
func New() *Settings {
	return &Settings{hidden: make(map[string]map[string]bool)}
}

// HiddenPanels возвращает копию множества скрытых панелей субъекта.
func (s *Settings) HiddenPanels(_ context.Context, actor string) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool)
	for panel, hidden := range s.hidden[strings.TrimSpace(actor)] {
		out[panel] = hidden
	}
	return out, nil
}

// SetPanelHidden задаёт признак скрытия панели субъекта. Пустой субъект или
// панель не сохраняются: настройка без владельца смысла не имеет.
func (s *Settings) SetPanelHidden(_ context.Context, actor, panelID string, hidden bool) error {
	actor = strings.TrimSpace(actor)
	panelID = strings.TrimSpace(panelID)
	if actor == "" || panelID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hidden[actor] == nil {
		s.hidden[actor] = make(map[string]bool)
	}
	s.hidden[actor][panelID] = hidden
	return nil
}
