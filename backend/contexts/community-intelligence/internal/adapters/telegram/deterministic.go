package telegram

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Message — заданное сообщение детерминированного источника. Author принадлежит
// красной зоне и в выжимку не попадает.
type Message struct {
	ID       domain.MessageID
	Author   string
	Text     string
	PostedAt time.Time
}

// feed — заданное состояние одного сообщества.
type feed struct {
	size                  int
	participantsAvailable bool
	messages              []ports.ReadMessage
}

// Deterministic — адаптер порта ports.CommunityReader с заранее заданным
// содержимым. Он не ходит в сеть и воспроизводит результат от вызова к вызову,
// поэтому служит развилкой среза: сценарии и тесты работают без MTProto.
//
// SessionValid управляет имитацией отсутствия сессии: при ложном значении чтение
// отклоняется так же, как это сделал бы реальный источник.
type Deterministic struct {
	// SessionValid — действительна ли авторизованная сессия. По умолчанию true.
	SessionValid bool

	mu          sync.RWMutex
	communities map[domain.CommunityID]feed
}

// Deterministic реализует порт чтения.
var _ ports.CommunityReader = (*Deterministic)(nil)

// NewDeterministic создаёт источник с действительной сессией и без сообществ.
func NewDeterministic() *Deterministic {
	return &Deterministic{
		SessionValid: true,
		communities:  make(map[domain.CommunityID]feed),
	}
}

// SetCommunity задаёт состояние сообщества: размер, доступность состава и
// сообщения. Сообщения фильтруются по периоду запроса при чтении.
func (d *Deterministic) SetCommunity(id domain.CommunityID, size int, participantsAvailable bool, messages []Message) {
	normalized := make([]ports.ReadMessage, 0, len(messages))
	for _, msg := range messages {
		normalized = append(normalized, ports.ReadMessage{
			MessageID: msg.ID,
			Author:    msg.Author,
			Text:      msg.Text,
			PostedAt:  msg.PostedAt,
		})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.communities[id] = feed{
		size:                  size,
		participantsAvailable: participantsAvailable,
		messages:              normalized,
	}
}

// ReadCommunity реализует ports.CommunityReader. Сообщения отбираются по периоду
// [from, to) и ограничиваются MaxMessages.
func (d *Deterministic) ReadCommunity(_ context.Context, req ports.ReadRequest) (ports.ReadResult, error) {
	if !d.SessionValid {
		return ports.ReadResult{}, fmt.Errorf("%w: MTProto-сессия недействительна", ports.ErrSessionRequired)
	}
	d.mu.RLock()
	current, ok := d.communities[req.Source.ID()]
	d.mu.RUnlock()
	if !ok {
		return ports.ReadResult{}, fmt.Errorf("%w: %s", ports.ErrCommunityUnavailable, req.Source.ID())
	}

	selected := make([]ports.ReadMessage, 0, len(current.messages))
	for _, msg := range current.messages {
		if !req.Period.From().IsZero() && msg.PostedAt.Before(req.Period.From()) {
			continue
		}
		if !req.Period.To().IsZero() && !msg.PostedAt.Before(req.Period.To()) {
			continue
		}
		selected = append(selected, msg)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if !selected[i].PostedAt.Equal(selected[j].PostedAt) {
			return selected[i].PostedAt.Before(selected[j].PostedAt)
		}
		return selected[i].MessageID < selected[j].MessageID
	})
	if req.MaxMessages > 0 && len(selected) > req.MaxMessages {
		selected = selected[:req.MaxMessages]
	}
	return ports.ReadResult{
		Messages:              selected,
		CommunitySize:         current.size,
		ParticipantsAvailable: current.participantsAvailable,
	}, nil
}
