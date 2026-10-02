package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// systemActor — актор по умолчанию для фонового исполнителя рендеринга, если
// вызывающий не назвал своего.
const systemActor domain.ActorID = "system"

// ProcessRender — команда исполнения одного задания рендеринга воркером.
type ProcessRender struct {
	RenderJobID domain.RenderJobID
	Actor       domain.ActorID
	At          time.Time

	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// ProcessRenderResult — результат исполнения задания.
type ProcessRenderResult struct {
	RenderJobID domain.RenderJobID
	State       domain.RenderState
	ArtifactRef string
	Reason      string
	// AlreadyDone — задание уже было в терминальном состоянии и не переигрывалось.
	AlreadyDone bool
	Events      []domain.DomainEvent
}

// ProcessRenderHandler — use case исполнения задания рендеринга.
type ProcessRenderHandler struct {
	uow      ports.UnitOfWork
	renderer ports.Renderer
	clock    ports.Clock
	ids      ports.IDGenerator
}

// NewProcessRenderHandler собирает use case из порта транзакции, порта движка
// рендеринга, часов и генератора идентификаторов событий.
func NewProcessRenderHandler(uow ports.UnitOfWork, renderer ports.Renderer, clock ports.Clock, ids ports.IDGenerator) *ProcessRenderHandler {
	return &ProcessRenderHandler{uow: uow, renderer: renderer, clock: clock, ids: ids}
}

// Handle исполняет задание: переводит его в rendering, вызывает движок за портом
// Renderer и завершает артефактом (событие content-rendered) либо отказом.
//
// В срезе вызов движка выполняется внутри транзакции: движок детерминированный и
// локальный. Для реального внешнего движка вызов обязан быть вынесен за пределы
// транзакции, а её границы — спроектированы под идемпотентный повтор.
func (h *ProcessRenderHandler) Handle(ctx context.Context, cmd ProcessRender) (ProcessRenderResult, error) {
	at := resolveAt(cmd.At, h.clock)
	actor := cmd.Actor
	if strings.TrimSpace(string(actor)) == "" {
		actor = systemActor
	}
	correlation := correlationOrDefault(cmd.CorrelationID, string(cmd.RenderJobID))

	var result ProcessRenderResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		job, err := tx.RenderJobs().FindByID(ctx, cmd.RenderJobID)
		if err != nil {
			return err
		}
		if job.State().IsTerminal() {
			result = ProcessRenderResult{
				RenderJobID: job.ID(),
				State:       job.State(),
				Reason:      job.Reason(),
				AlreadyDone: true,
			}
			return nil
		}

		pkg, err := tx.Packages().FindByID(ctx, job.AssetID())
		if err != nil {
			return err
		}
		version, ok := pkg.FindVersion(job.VersionID())
		if !ok {
			return fmt.Errorf("%w: %s", ErrVersionRequired, job.VersionID())
		}

		loadedJobVersion := job.Version()
		if err := job.Start(domain.RenderTransitionInput{Actor: actor, At: at}); err != nil {
			return err
		}

		rendered, renderErr := h.renderer.Render(ctx, ports.RenderRequest{
			AssetID:    job.AssetID(),
			VersionID:  job.VersionID(),
			TemplateID: job.TemplateID(),
			Format:     job.Format(),
			Locale:     version.Locale(),
			Body:       version.Body(),
		})
		if renderErr != nil {
			if failErr := job.Fail(domain.RenderTransitionInput{Actor: actor, At: at, Reason: renderReason(renderErr)}); failErr != nil {
				return failErr
			}
			if err := tx.RenderJobs().Save(ctx, job, loadedJobVersion); err != nil {
				return err
			}
			result = ProcessRenderResult{
				RenderJobID: job.ID(),
				State:       job.State(),
				Reason:      job.Reason(),
			}
			return nil
		}

		artifact, buildErr := buildArtifact(job, rendered)
		if buildErr != nil {
			if failErr := job.Fail(domain.RenderTransitionInput{Actor: actor, At: at, Reason: renderReason(buildErr)}); failErr != nil {
				return failErr
			}
			if err := tx.RenderJobs().Save(ctx, job, loadedJobVersion); err != nil {
				return err
			}
			result = ProcessRenderResult{
				RenderJobID: job.ID(),
				State:       job.State(),
				Reason:      job.Reason(),
			}
			return nil
		}

		if err := attachArtifact(ctx, tx, pkg, artifact); err != nil {
			return err
		}
		if err := job.Complete(domain.RenderTransitionInput{
			Actor:         actor,
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: correlation,
			CausationID:   cmd.CausationID,
		}, artifact); err != nil {
			return err
		}
		if err := tx.RenderJobs().Save(ctx, job, loadedJobVersion); err != nil {
			return err
		}
		events := job.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		job.PullDomainEvents()
		result = ProcessRenderResult{
			RenderJobID: job.ID(),
			State:       job.State(),
			ArtifactRef: artifact.Ref().String(),
			Events:      events,
		}
		return nil
	})
	if err != nil {
		return ProcessRenderResult{}, err
	}
	return result, nil
}

// attachArtifact закрепляет наполнение лендинга или документ за контент-пакетом.
// Презентация хранится в самом задании рендеринга, поэтому пакет не меняется.
func attachArtifact(ctx context.Context, tx ports.Transaction, pkg *domain.ContentPackage, artifact domain.RenderedArtifact) error {
	switch artifact.Format() {
	case domain.RenderFormatLandingPage:
		landing, ok := artifact.Landing()
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrArtifactFormatMismatch, artifact.Format())
		}
		loadedVersion := pkg.Version()
		if err := pkg.AttachLandingContent(landing); err != nil {
			return err
		}
		return tx.Packages().Save(ctx, pkg, loadedVersion)
	case domain.RenderFormatDocument:
		document, ok := artifact.Document()
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrArtifactFormatMismatch, artifact.Format())
		}
		loadedVersion := pkg.Version()
		if err := pkg.AttachDocument(document); err != nil {
			return err
		}
		return tx.Packages().Save(ctx, pkg, loadedVersion)
	default:
		return nil
	}
}

// buildArtifact собирает артефакт нужного формата из результата движка. Ссылка на
// артефакт: для лендинга и документа — пакет (по нему читается наполнение), для
// презентации — задание рендеринга.
func buildArtifact(job *domain.RenderJob, rendered ports.RenderResult) (domain.RenderedArtifact, error) {
	switch job.Format() {
	case domain.RenderFormatLandingPage:
		landing, err := domain.NewLandingContent(
			domain.LandingContentID("lc-"+job.ID().String()),
			job.VersionID(),
			rendered.Title,
			rendered.Headline,
			rendered.Intro,
			rendered.Sections,
		)
		if err != nil {
			return domain.RenderedArtifact{}, err
		}
		return domain.NewLandingArtifact(domain.ArtifactRef(job.AssetID().String()), landing), nil
	case domain.RenderFormatDocument:
		document, err := domain.NewDocument(
			domain.DocumentID("doc-"+job.ID().String()),
			job.VersionID(),
			rendered.Title,
			rendered.Body,
		)
		if err != nil {
			return domain.RenderedArtifact{}, err
		}
		return domain.NewDocumentArtifact(domain.ArtifactRef(job.AssetID().String()), document), nil
	case domain.RenderFormatPresentation:
		presentation, err := domain.NewPresentation(rendered.Title, rendered.Slides)
		if err != nil {
			return domain.RenderedArtifact{}, err
		}
		return domain.NewPresentationArtifact(domain.ArtifactRef(job.ID().String()), presentation), nil
	default:
		return domain.RenderedArtifact{}, fmt.Errorf("%w: %q", domain.ErrUnknownRenderFormat, job.Format())
	}
}

// renderReason возвращает причину отказа рендера, различая недоступность движка и
// его отказ по качеству. Пустая причина не допускается доменом.
func renderReason(err error) string {
	if err == nil {
		return "движок рендеринга не сообщил причину"
	}
	if strings.TrimSpace(err.Error()) == "" {
		return "движок рендеринга не сообщил причину"
	}
	return err.Error()
}
