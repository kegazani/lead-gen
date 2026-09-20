package contracts

import (
	"fmt"
	"strings"
	"time"
)

// ContractAudienceSnapshot — имя потребляемого контракта-запроса: снимок
// аудитории, владельцем которого является контекст audience (каталог связей,
// связь audience-to-outreach, интеграция query, транспорт sync/http-json).
const ContractAudienceSnapshot = "audience-snapshot"

// AudienceSnapshotSchemaVersion — текущая версия схемы. Изменять по правилам
// semver из envelope.go.
const AudienceSnapshotSchemaVersion = "1.0.0"

// AudienceSnapshot — антикоррупционный DTO ПОТРЕБИТЕЛЯ для контракта
// audience-snapshot.
//
// Владелец контракта — контекст audience; его Go-типы в этот контекст не
// импортируются. Потребитель объявляет здесь ровно те поля, которые нужны ему
// как ссылку на вход: идентификатор снимка, сегмент, версию и границу свежести.
// Содержимое сегмента (список контактов и персональные данные) через границу не
// проходит: снимок разрешается на стороне владельца, а не копируется сюда.
type AudienceSnapshot struct {
	Envelope

	// SnapshotID — идентификатор неизменяемого снимка аудитории.
	SnapshotID string `json:"snapshot_id"`
	// SegmentID — ссылка на сегмент, из которого построен снимок.
	SegmentID string `json:"segment_id"`
	// Version — версия снимка; растёт при пересборке сегмента.
	Version int64 `json:"version"`
	// MemberCount — размер снимка. Ноль допустим: пустой сегмент — валидный
	// снимок, а не ошибка контракта.
	MemberCount int64 `json:"member_count"`
	// GeneratedAt — момент построения снимка; используется как граница свежести.
	GeneratedAt time.Time `json:"generated_at"`
}

// Validate проверяет envelope и обязательные поля контракта, собирая все
// отсутствующие или некорректные поля, а не первое из них.
func (s AudienceSnapshot) Validate() error {
	if err := s.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if s.Name != ContractAudienceSnapshot {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(s.SnapshotID) == "" {
		invalid = append(invalid, "snapshot_id")
	}
	if strings.TrimSpace(s.SegmentID) == "" {
		invalid = append(invalid, "segment_id")
	}
	if s.Version <= 0 {
		invalid = append(invalid, "version")
	}
	if s.MemberCount < 0 {
		invalid = append(invalid, "member_count")
	}
	if s.GeneratedAt.IsZero() {
		invalid = append(invalid, "generated_at")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("contracts: audience-snapshot: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}
