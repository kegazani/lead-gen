package domain

import (
	"fmt"
	"time"
)

// DraftState — состояние черновика действия.
//
// Переходы: предложено → ожидает утверждения → утверждено / отклонено →
// исполнено / ошибка. Любой другой переход запрещён; черновик, доработанный
// после решения, возвращается в «ожидает утверждения» и требует нового решения.
type DraftState string

const (
	// DraftStateProposed — черновик создан и ещё не поставлен на утверждение.
	DraftStateProposed DraftState = "proposed"

	// DraftStatePendingApproval — черновик в очереди утверждения.
	DraftStatePendingApproval DraftState = "pending-approval"

	// DraftStateApproved — по точной версии черновика есть положительное решение.
	DraftStateApproved DraftState = "approved"

	// DraftStateRejected — черновик возвращён на доработку с причиной.
	DraftStateRejected DraftState = "rejected"

	// DraftStateExpired — срок утверждения истёк; автоутверждение запрещено.
	DraftStateExpired DraftState = "expired"

	// DraftStateExecuted — утверждённое действие выполнено.
	DraftStateExecuted DraftState = "executed"

	// DraftStateFailed — исполнение утверждённого действия завершилось ошибкой.
	DraftStateFailed DraftState = "failed"
)

// IsTerminal сообщает, что черновик вышел из жизненного цикла исполнения и
// больше не может быть доработан.
func (s DraftState) IsTerminal() bool {
	switch s {
	case DraftStateExecuted, DraftStateFailed:
		return true
	default:
		return false
	}
}

// isValid сообщает, что состояние черновика объявлено доменом.
func (s DraftState) isValid() bool {
	switch s {
	case DraftStateProposed, DraftStatePendingApproval, DraftStateApproved,
		DraftStateRejected, DraftStateExpired, DraftStateExecuted, DraftStateFailed:
		return true
	default:
		return false
	}
}

// DraftSpec — состав черновика: материал, сегмент, адресат и канал.
// Заполняется целиком; частичный состав в домен не проходит.
type DraftSpec struct {
	Material  MaterialID
	Segment   SegmentID
	Recipient RecipientID
	Channel   ActionChannel
}

func (s DraftSpec) validate() error {
	if err := s.Material.validate(); err != nil {
		return err
	}
	if err := s.Segment.validate(); err != nil {
		return err
	}
	if err := s.Recipient.validate(); err != nil {
		return err
	}
	if s.Channel.IsZero() {
		return fmt.Errorf("%w: канал черновика не задан", ErrEmptyDraftField)
	}
	return nil
}

// ActionDraft — сущность агрегата campaign: подготовленное, но ещё не
// разрешённое исходящее действие.
// DraftEdit — запись правки черновика человеком: автор, время, прежний и новый
// состав. Хранится отдельно от сгенерированного варианта (BRD 1.1): исходный
// состав черновика (From) сохраняется даже после того, как черновик заменён
// правкой, — для последующего анализа качества генерации.
type DraftEdit struct {
	Actor ActorID
	At    time.Time
	From  DraftSpec
	To    DraftSpec
}

// ActionDraft — сущность агрегата campaign: подготовленное, но ещё не разрешённое исходящее действие.
//
// Версия черновика — естественный ключ идемпотентности решения и защиты
// инварианта I2: каждая правка повышает версию, поэтому прежнее решение к
// новой версии не относится. Поля закрыты, состояние меняется только методами
// перехода, а исполнение возможно только из состояния «утверждено».
type ActionDraft struct {
	id       DraftID
	campaign CampaignID
	spec     DraftSpec
	state    DraftState
	version  int

	// edits — правки, внесённые человеком: отдельная от сгенерированного запись
	// (автор, время, прежний и новый состав).
	edits []DraftEdit

	// postponedUntil — момент, до которого оператор отложил решение. Нулевое
	// значение означает, что черновик не отложен.
	postponedUntil time.Time
}

// newActionDraft создаёт черновик в состоянии «предложено» с версией 1.
func newActionDraft(id DraftID, campaign CampaignID, spec DraftSpec) (*ActionDraft, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := campaign.validate(); err != nil {
		return nil, err
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	return &ActionDraft{
		id:       id,
		campaign: campaign,
		spec:     spec,
		state:    DraftStateProposed,
		version:  1,
	}, nil
}

// ID возвращает идентификатор черновика.
func (d *ActionDraft) ID() DraftID { return d.id }

// CampaignID возвращает кампанию-владельца.
func (d *ActionDraft) CampaignID() CampaignID { return d.campaign }

// State возвращает текущее состояние.
func (d *ActionDraft) State() DraftState { return d.state }

// Version возвращает текущую версию черновика.
func (d *ActionDraft) Version() int { return d.version }

// Channel возвращает канал действия.
func (d *ActionDraft) Channel() ActionChannel { return d.spec.Channel }

// Spec возвращает состав черновика.
func (d *ActionDraft) Spec() DraftSpec { return d.spec }

// checkVersion сверяет ожидаемую версию с текущей (инвариант I2).
func (d *ActionDraft) checkVersion(expected int) error {
	if expected != d.version {
		return fmt.Errorf(
			"%w: черновик %s ожидал версию %d, текущая %d",
			ErrDraftVersionMismatch, d.id, expected, d.version,
		)
	}
	return nil
}

// submit переводит черновик из «предложено» в «ожидает утверждения».
func (d *ActionDraft) submit() error {
	if d.state != DraftStateProposed {
		return d.invalidTransition(DraftStatePendingApproval)
	}
	d.state = DraftStatePendingApproval
	return nil
}

// approve переводит поставленный на утверждение черновик в «утверждено».
func (d *ActionDraft) approve() error {
	if d.state != DraftStatePendingApproval {
		return d.invalidTransition(DraftStateApproved)
	}
	d.state = DraftStateApproved
	d.postponedUntil = time.Time{}
	return nil
}

// reject переводит поставленный на утверждение черновик в «отклонено».
func (d *ActionDraft) reject() error {
	if d.state != DraftStatePendingApproval {
		return d.invalidTransition(DraftStateRejected)
	}
	d.state = DraftStateRejected
	d.postponedUntil = time.Time{}
	return nil
}

// postpone откладывает решение по черновику до указанного момента. Черновик
// остаётся «ожидает утверждения» и не исполняется: отложение не является ни
// решением, ни автоутверждением. Нулевой срок отложения недопустим.
func (d *ActionDraft) postpone(until time.Time) error {
	if until.IsZero() {
		return fmt.Errorf("%w: черновик %s", ErrPostponeDeadlineRequired, d.id)
	}
	if d.state != DraftStatePendingApproval {
		return d.invalidTransition(DraftStatePendingApproval)
	}
	d.postponedUntil = until.UTC()
	return nil
}

// revise применяет доработку: заменяет состав, повышает версию и возвращает
// черновик на утверждение. Прежнее решение к новой версии не относится.
// Исполненный или сбойный черновик доработке не подлежит.
func (d *ActionDraft) revise(spec DraftSpec) error {
	return d.applyRevision(spec, nil)
}

// reviseRecorded применяет доработку и сохраняет запись правки с автором и
// временем. Изменение состава и запись правки — одна операция: правка без
// записи автора не создаётся.
func (d *ActionDraft) reviseRecorded(spec DraftSpec, edit DraftEdit) error {
	return d.applyRevision(spec, &edit)
}

// applyRevision — общий путь доработки. При непустой записи правки она
// добавляется к истории после успешной замены состава, поэтому запрещённый
// переход не оставляет записи.
func (d *ActionDraft) applyRevision(spec DraftSpec, edit *DraftEdit) error {
	switch d.state {
	case DraftStateProposed, DraftStatePendingApproval, DraftStateApproved, DraftStateRejected:
	default:
		return d.invalidTransition(DraftStatePendingApproval)
	}
	if err := spec.validate(); err != nil {
		return err
	}
	d.spec = spec
	d.version++
	d.state = DraftStatePendingApproval
	// Доработка снимает отложение: решение по новой версии оператор принимает заново.
	d.postponedUntil = time.Time{}
	if edit != nil {
		d.edits = append(d.edits, *edit)
	}
	return nil
}

// expire помечает черновик просроченным. Автоутверждение не выполняется:
// черновик остаётся неисполненным.
func (d *ActionDraft) expire() error {
	if d.state != DraftStatePendingApproval {
		return d.invalidTransition(DraftStateExpired)
	}
	d.state = DraftStateExpired
	d.postponedUntil = time.Time{}
	return nil
}

// Edits возвращает записи правок черновика в порядке внесения. Возвращается
// копия: история отдельна от текущего (правленого) состава.
func (d *ActionDraft) Edits() []DraftEdit {
	return append([]DraftEdit(nil), d.edits...)
}

// PostponedUntil возвращает момент, до которого решение отложено. Нулевое
// значение означает, что черновик не отложен.
func (d *ActionDraft) PostponedUntil() time.Time { return d.postponedUntil }

// markExecuted переводит утверждённый черновик в «исполнено».
func (d *ActionDraft) markExecuted() error {
	if d.state != DraftStateApproved {
		return d.invalidTransition(DraftStateExecuted)
	}
	d.state = DraftStateExecuted
	return nil
}

// markFailed переводит утверждённый черновик в «ошибка».
func (d *ActionDraft) markFailed() error {
	if d.state != DraftStateApproved {
		return d.invalidTransition(DraftStateFailed)
	}
	d.state = DraftStateFailed
	return nil
}

func (d *ActionDraft) invalidTransition(to DraftState) error {
	return fmt.Errorf(
		"%w: черновик %s в состоянии %q не переходит в %q",
		ErrInvalidDraftTransition, d.id, d.state, to,
	)
}
