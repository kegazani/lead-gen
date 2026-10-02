// Package idgen предоставляет источники идентификаторов событий. Реализация не
// содержит доменных правил и используется по умолчанию, когда вызывающий не
// передал собственный генератор.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

// Random — генератор event_id на криптостойкой случайности.
type Random struct{}

// NewRandom возвращает генератор случайных идентификаторов.
func NewRandom() Random { return Random{} }

// NewEventID возвращает новый идентификатор события. Случайность криптостойкая:
// идентификатор служит ключом дедупликации у потребителя.
func (Random) NewEventID() domain.EventID {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand практически не отказывает; аварийный путь всё же не
		// возвращает пустой идентификатор, иначе событие нельзя было бы
		// опубликовать.
		return domain.EventID(fmt.Sprintf("evt-%d", time.Now().UnixNano()))
	}
	return domain.EventID(hex.EncodeToString(buf[:]))
}
