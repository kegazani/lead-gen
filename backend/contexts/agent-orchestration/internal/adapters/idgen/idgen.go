// Package idgen предоставляет источники идентификаторов событий и запусков.
// Реализация не содержит доменных правил и используется по умолчанию, когда
// вызывающий не передал собственный генератор.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// Random — генератор идентификаторов на криптостойкой случайности.
type Random struct{}

// NewRandom возвращает генератор случайных идентификаторов.
func NewRandom() Random { return Random{} }

// NewEventID возвращает новый идентификатор события. Случайность криптостойкая:
// идентификатор служит ключом дедупликации у потребителя.
func (Random) NewEventID() domain.EventID {
	return domain.EventID(randomHex("evt-"))
}

// NewRunID возвращает новый идентификатор запуска агента.
func (Random) NewRunID() domain.RunID {
	return domain.RunID(randomHex("run-"))
}

// randomHex возвращает hex-строку из 16 случайных байт с префиксом. При отказе
// crypto/rand аварийный путь всё равно не возвращает пустой идентификатор.
func randomHex(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(buf[:])
}
