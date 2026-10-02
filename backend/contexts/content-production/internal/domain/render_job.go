package domain

import (
	"fmt"
	"strings"
	"time"
)

// RenderState — состояние задания рендеринга.
//
// Модель: requested → rendering → rendered; из requested или rendering возможен
// отказ (failed). Терминальные состояния — rendered и failed; из них переходов
// нет.
type RenderState string

const (
	// RenderStateRequested — задание создано и ждёт исполнителя.
	RenderStateRequested RenderState = "requested"

	// RenderStateRendering — исполнитель приступил к рендеру.
	RenderStateRendering RenderState = "rendering"

	// RenderStateRendered — рендер завершён, артефакт готов (терминальное).
	RenderStateRendered RenderState = "rendered"

	// RenderStateFailed — рендер завершился отказом (терминальное).
	RenderStateFailed RenderState = "failed"
)

// RenderStates возвращает согласованный набор состояний задания.
func RenderStates() []RenderState {
	return []RenderState{RenderStateRequested, RenderStateRendering, RenderStateRendered, RenderStateFailed}
}

// isValid сообщает, что состояние объявлено доменом.
func (s RenderState) isValid() bool {
	switch s {
	case RenderStateRequested, RenderStateRendering, RenderStateRendered, RenderStateFailed:
		return true
	default:
		return false
	}
}

// IsTerminal сообщает, что состояние не допускает дальнейших переходов.
func (s RenderState) IsTerminal() bool {
	return s == RenderStateRendered || s == RenderStateFailed
}

// RenderJob — aggregate root render-job.
//
// Инвариант: рендер указывает точную версию контента и шаблон; оба неизменяемы
// после создания задания. Артефакт записывается только в состоянии rendering и
// только формата задания.
type RenderJob struct {
	id         RenderJobID
	tenantID   TenantID
	assetID    ContentAssetID
	versionID  ContentVersionID
	templateID TemplateID
	format     RenderFormat
	state      RenderState
	artifact   *RenderedArtifact
	reason     string
	version    int
	events     []DomainEvent
}

// NewRenderJobParams — данные создания задания рендеринга.
type NewRenderJobParams struct {
	ID         RenderJobID
	TenantID   TenantID
	AssetID    ContentAssetID
	VersionID  ContentVersionID
	TemplateID TemplateID
	Format     RenderFormat
}

// NewRenderJob создаёт задание в состоянии requested. Задание без точной версии
// контента и шаблона не создаётся.
func NewRenderJob(params NewRenderJobParams) (*RenderJob, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.AssetID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(params.VersionID)) == "" {
		return nil, ErrVersionRequired
	}
	if err := params.VersionID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(params.TemplateID)) == "" {
		return nil, ErrTemplateRequired
	}
	if err := params.Format.validate(); err != nil {
		return nil, err
	}
	return &RenderJob{
		id:         params.ID,
		tenantID:   params.TenantID,
		assetID:    params.AssetID,
		versionID:  params.VersionID,
		templateID: params.TemplateID,
		format:     params.Format,
		state:      RenderStateRequested,
		version:    1,
	}, nil
}

// RenderTransitionInput — данные перехода состояния задания.
type RenderTransitionInput struct {
	Actor ActorID
	At    time.Time
	// Reason обязателен для отказа.
	Reason string
	// EventID обязателен для перехода, порождающего событие (завершение рендера).
	EventID EventID

	CorrelationID CorrelationID
	CausationID   CausationID
}

// Start переводит задание из requested в rendering.
func (j *RenderJob) Start(input RenderTransitionInput) error {
	if j.state.IsTerminal() {
		return ErrTerminalRenderState
	}
	if j.state != RenderStateRequested {
		return fmt.Errorf("%w: %s → rendering", ErrInvalidRenderTransition, j.state)
	}
	if err := input.Actor.validate(); err != nil {
		return err
	}
	if input.At.IsZero() {
		return ErrMissingTime
	}
	j.state = RenderStateRendering
	j.version++
	return nil
}

// Complete переводит задание из rendering в rendered и фиксирует артефакт.
// Артефакт обязан соответствовать формату задания. Завершение порождает событие
// content-rendered.
func (j *RenderJob) Complete(input RenderTransitionInput, artifact RenderedArtifact) error {
	if j.state.IsTerminal() {
		return ErrTerminalRenderState
	}
	if j.state != RenderStateRendering {
		return fmt.Errorf("%w: %s → rendered", ErrInvalidRenderTransition, j.state)
	}
	if err := input.Actor.validate(); err != nil {
		return err
	}
	header, err := newEventHeader(j.tenantID, input.At, input.EventID, input.CorrelationID, input.CausationID)
	if err != nil {
		return err
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	if artifact.Format() != j.format {
		return fmt.Errorf("%w: задание %s, артефакт %s", ErrArtifactFormatMismatch, j.format, artifact.Format())
	}
	j.state = RenderStateRendered
	j.artifact = &artifact
	j.version++
	j.events = append(j.events, ContentRendered{
		EventHeader: header,
		RenderJobID: j.id,
		AssetID:     j.assetID,
		VersionID:   j.versionID,
		TemplateID:  j.templateID,
		Format:      j.format,
		ArtifactRef: artifact.Ref(),
	})
	return nil
}

// Fail переводит задание из requested или rendering в failed. Причина обязательна.
func (j *RenderJob) Fail(input RenderTransitionInput) error {
	if j.state.IsTerminal() {
		return ErrTerminalRenderState
	}
	if err := input.Actor.validate(); err != nil {
		return err
	}
	if input.At.IsZero() {
		return ErrMissingTime
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return ErrReasonRequired
	}
	j.state = RenderStateFailed
	j.reason = reason
	j.version++
	return nil
}

// ID возвращает идентификатор задания.
func (j *RenderJob) ID() RenderJobID { return j.id }

// TenantID возвращает тенанта-владельца задания.
func (j *RenderJob) TenantID() TenantID { return j.tenantID }

// AssetID возвращает контент-пакет, который рендерится.
func (j *RenderJob) AssetID() ContentAssetID { return j.assetID }

// VersionID возвращает точную версию контента.
func (j *RenderJob) VersionID() ContentVersionID { return j.versionID }

// TemplateID возвращает шаблон рендера.
func (j *RenderJob) TemplateID() TemplateID { return j.templateID }

// Format возвращает целевой формат артефакта.
func (j *RenderJob) Format() RenderFormat { return j.format }

// State возвращает состояние задания.
func (j *RenderJob) State() RenderState { return j.state }

// Artifact возвращает артефакт и признак его наличия (только после rendered).
func (j *RenderJob) Artifact() (RenderedArtifact, bool) {
	if j.artifact == nil {
		return RenderedArtifact{}, false
	}
	return *j.artifact, true
}

// Reason возвращает причину отказа (только после failed).
func (j *RenderJob) Reason() string { return j.reason }

// Version возвращает версию агрегата — токен оптимистичной блокировки.
func (j *RenderJob) Version() int { return j.version }

// DomainEvents возвращает копию неподтверждённых событий.
func (j *RenderJob) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), j.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (j *RenderJob) PullDomainEvents() []DomainEvent {
	events := j.events
	j.events = nil
	return events
}
