// Package idgen предоставляет источники идентификаторов тенанта, пользователя,
// события и заявки. Реализация не содержит доменных правил и используется по
// умолчанию, когда вызывающий не передал собственный генератор.
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// Random — генератор идентификаторов на криптостойкой случайности.
type Random struct{}

// NewRandom возвращает генератор случайных идентификаторов.
func NewRandom() Random { return Random{} }

// NewTenantID возвращает новый идентификатор тенанта.
func (Random) NewTenantID() domain.TenantID { return domain.TenantID(newID("tenant")) }

// NewUserID возвращает новый идентификатор пользователя.
func (Random) NewUserID() domain.UserID { return domain.UserID(newID("user")) }

// NewEventID возвращает новый идентификатор события. Случайность криптостойкая:
// идентификатор служит ключом дедупликации у потребителя.
func (Random) NewEventID() domain.EventID { return domain.EventID(newID("evt")) }

// NewAuditRecordID возвращает новый идентификатор записи аудита.
func (Random) NewAuditRecordID() domain.AuditRecordID {
	return domain.AuditRecordID(newID("audit"))
}

// NewRequestID возвращает новый идентификатор заявки.
func (Random) NewRequestID() string { return newID("req") }

// NewOfferID возвращает новый идентификатор версии оффера.
func (Random) NewOfferID() string { return newID("offer") }

// newID строит идентификатор с префиксом. Аварийный путь не возвращает пустую
// строку: идентификатор должен быть пригоден для дедупликации и ссылок.
func newID(prefix string) string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])
}
