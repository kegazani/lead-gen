package segmentmembership

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
const ContractName = "segment-membership"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// Envelope — общий конверт контрактов контекста audience.
type Envelope = envelope.Envelope

// ErrNotFound — сегмента у владельца нет либо он принадлежит другому тенанту.
var ErrNotFound = errors.New("segmentmembership: сегмент не найден")

// ErrUnavailable — владелец сегмента недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех и не выдумывает состав.
var ErrUnavailable = errors.New("segmentmembership: владелец сегмента недоступен")

// SegmentMembership — состав сегмента аудитории на текущей версии. DTO служит и
// формой изменения состава: Version и ChangedAt показывают, к какому срезу
// относятся Members.
type SegmentMembership struct {
	Envelope

	// SegmentID — ссылка на сегмент-владелец состава.
	SegmentID string `json:"segment_id"`
	// Version — версия сегмента, к которой относится состав.
	Version int64 `json:"version"`
	// MemberCount — размер состава.
	MemberCount int64 `json:"member_count"`
	// Members — ссылки на лидов состава. Допускается опускать, когда потребителю
	// нужен только размер; при передаче размер обязан совпадать с MemberCount.
	Members []string `json:"members,omitempty"`
	// ChangedAt — момент последнего изменения состава. Пусто, если состав ещё ни
	// разу не менялся: у сегмента без изменений нет момента изменения.
	ChangedAt time.Time `json:"changed_at,omitempty"`
}

// Validate проверяет конверт и обязательные поля контракта, собирая все
// отсутствующие или некорректные поля, а не первое из них.
func (m SegmentMembership) Validate() error {
	if err := m.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if m.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(m.SegmentID) == "" {
		invalid = append(invalid, "segment_id")
	}
	if m.Version <= 0 {
		invalid = append(invalid, "version")
	}
	if m.MemberCount < 0 {
		invalid = append(invalid, "member_count")
	}
	if m.Members != nil && int64(len(m.Members)) != m.MemberCount {
		invalid = append(invalid, "members")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("segmentmembership: segment-membership: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Port — запрос состава сегмента у владельца контракта. ErrNotFound и
// ErrUnavailable — исходы владельца, а не паника транспорта.
type Port interface {
	// Membership возвращает состав сегмента по идентификатору. Конверт вызывающего
	// задаёт сквозную корреляцию и тенант.
	Membership(ctx context.Context, env Envelope, segmentID string) (SegmentMembership, error)
}

// InProcHandler — функции прямого вызова владельца для co-location и тестов.
// Сетевой адаптер при разделении сервисов даёт ту же форму.
type InProcHandler struct {
	// MembershipFunc реализует запрос состава сегмента.
	MembershipFunc func(ctx context.Context, env Envelope, segmentID string) (SegmentMembership, error)
}

// Membership реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает состав.
func (h InProcHandler) Membership(ctx context.Context, env Envelope, segmentID string) (SegmentMembership, error) {
	if h.MembershipFunc == nil {
		return SegmentMembership{}, ErrUnavailable
	}
	return h.MembershipFunc(ctx, env, segmentID)
}

// InProc заворачивает обработчики в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
