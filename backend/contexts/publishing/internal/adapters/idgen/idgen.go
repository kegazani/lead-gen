// Package idgen реализует генератор идентификаторов событий контекста publishing
// поверх crypto/rand.
package idgen

import (
	"crypto/rand"
	"encoding/hex"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Random — генератор идентификаторов событий на случайных байтах.
type Random struct{}

// Random реализует порт генератора идентификаторов событий.
var _ ports.IDGenerator = Random{}

// NewRandom создаёт генератор.
func NewRandom() Random { return Random{} }

// NewEventID реализует ports.IDGenerator. Источник случайности недоступен —
// возвращается пустой идентификатор: событие без event_id не будет подтверждено
// outbox, и отказ виден, а не выдан за успех.
func (Random) NewEventID() domain.EventID {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return domain.EventID(hex.EncodeToString(buf[:]))
}
