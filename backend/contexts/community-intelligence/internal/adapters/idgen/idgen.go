// Package idgen предоставляет источники идентификаторов событий и выжимок.
// Реализация не содержит доменных правил и используется по умолчанию, когда
// вызывающий не передал собственный генератор.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// Random — генератор идентификаторов на криптостойкой случайности.
type Random struct{}

// NewRandom возвращает генератор случайных идентификаторов.
func NewRandom() Random { return Random{} }

// NewEventID возвращает новый идентификатор события.
func (Random) NewEventID() domain.EventID {
	return domain.EventID(randomHex("evt"))
}

// NewDigestID возвращает новый идентификатор выжимки.
func (Random) NewDigestID() domain.DigestID {
	return domain.DigestID(randomHex("dgst"))
}

// randomHex возвращает идентификатор с префиксом на криптостойкой случайности.
// crypto/rand практически не отказывает; аварийный путь всё равно не возвращает
// пустой идентификатор, иначе объект нельзя было бы сохранить или опубликовать.
func randomHex(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}
