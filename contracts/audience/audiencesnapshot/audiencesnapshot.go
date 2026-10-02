package audiencesnapshot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/contracts/audience/envelope"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "audience-snapshot"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// Envelope — общий конверт контрактов контекста audience.
type Envelope = envelope.Envelope

// ErrNotFound — снимка у владельца нет либо он принадлежит другому тенанту.
// Существование чужого снимка этим отказом не раскрывается.
var ErrNotFound = errors.New("audiencesnapshot: снимок не найден")

// ErrUnavailable — владелец снимка недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает снимок.
var ErrUnavailable = errors.New("audiencesnapshot: владелец снимка недоступен")

// AudienceSnapshot — ссылка на неизменяемый снимок аудитории.
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

// Validate проверяет конверт и обязательные поля контракта, собирая все
// отсутствующие или некорректные поля, а не первое из них.
func (s AudienceSnapshot) Validate() error {
	if err := s.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if s.Name != ContractName {
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
		return fmt.Errorf("audiencesnapshot: audience-snapshot: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Port — запрос снимка аудитории у владельца контракта. ErrNotFound и
// ErrUnavailable — исходы владельца, а не паника транспорта.
type Port interface {
	// Fetch возвращает снимок по идентификатору. Конверт вызывающего задаёт
	// сквозную корреляцию и тенант.
	Fetch(ctx context.Context, env Envelope, snapshotID string) (AudienceSnapshot, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// FetchFunc реализует запрос снимка.
	FetchFunc func(ctx context.Context, env Envelope, snapshotID string) (AudienceSnapshot, error)
}

// Fetch реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает снимок.
func (h InProcHandler) Fetch(ctx context.Context, env Envelope, snapshotID string) (AudienceSnapshot, error) {
	if h.FetchFunc == nil {
		return AudienceSnapshot{}, ErrUnavailable
	}
	return h.FetchFunc(ctx, env, snapshotID)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
