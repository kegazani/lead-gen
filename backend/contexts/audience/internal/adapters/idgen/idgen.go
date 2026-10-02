// Package idgen предоставляет источники идентификаторов событий и снимков
// аудитории. Реализация не содержит доменных правил и используется по умолчанию,
// когда вызывающий не передал собственный генератор.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// Random — генератор идентификаторов на криптостойкой случайности.
type Random struct{}

// NewRandom возвращает генератор случайных идентификаторов.
func NewRandom() Random { return Random{} }

// NewEventID возвращает новый идентификатор события. Случайность криптостойкая:
// идентификатор служит ключом дедупликации у потребителя.
func (Random) NewEventID() domain.EventID {
	return domain.EventID(randomID("evt"))
}

// NewSnapshotID возвращает новый идентификатор снимка аудитории.
func (Random) NewSnapshotID() domain.SnapshotID {
	return domain.SnapshotID(randomID("snap"))
}

// randomID возвращает идентификатор с префиксом и криптостойкой случайной частью.
func randomID(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand практически не отказывает; аварийный путь всё же не
		// возвращает пустой идентификатор, иначе объект нельзя было бы сохранить.
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}
