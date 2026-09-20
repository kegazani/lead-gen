package command

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// BuildBatch — сценарий формирования состава пакета для показа оператору.
//
// Состав формирует чистый домен: прикладной слой загружает окно черновиков
// через ports.DraftWindowRepository (keyset-пагинация), передаёт лимиты
// (design.md, Решение 7 — конфигурация, а не доменные константы) и внешний
// предикат риска, а затем сериализует результат. Полная загрузка кампании на
// 50 000 черновиков здесь не происходит (design.md, Решение 6): кого включать и
// кого исключать, решает домен по окну; здесь нет ни одного доменного правила.
//
// Лимиты приходят параметром, а не читаются из конфигурации в этом слое: их
// готовит вызывающий (композиция/конфигурация пакетных лимитов).
type BuildBatch struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID
	Limits     domain.BatchLimits

	// After — исключающий курсор окна: продолжение после предыдущей страницы.
	// Нулевой курсор означает начало очереди.
	After ports.DraftWindowCursor
}

// BatchCompositionEntry — позиция состава: черновик и его точная версия.
type BatchCompositionEntry struct {
	DraftID domain.DraftID
	Version int
	Spec    domain.DraftSpec
}

// BatchDifference — отличие позиции от образца содержимого пакета. Признак
// однородности гарантирует совпадение материала и сегмента, поэтому отличия
// ожидаются в адресате и канале; перечень показывается оператору до решения.
type BatchDifference struct {
	DraftID domain.DraftID
	Field   string
	From    string
	To      string
}

// BatchComposition — показ одного шарда пакета: основание однородности, число
// черновиков, digest состава, образец содержимого и перечень отличий.
type BatchComposition struct {
	Key         domain.HomogeneityKey
	Digest      string
	Size        int
	Sample      domain.DraftSpec
	Entries     []BatchCompositionEntry
	Differences []BatchDifference
}

// ExcludedComposition — черновик, исключённый доменом, с причинами по каждому
// признаку. Исключённый черновик не отбрасывается молча и остаётся на
// индивидуальное рассмотрение.
type ExcludedComposition struct {
	DraftID domain.DraftID
	Version int
	Reasons []domain.RiskReason
}

// IndividualComposition — черновик, направленный на индивидуальное рассмотрение:
// однородных оказалось меньше минимального размера пакета.
type IndividualComposition struct {
	DraftID domain.DraftID
	Version int
	Spec    domain.DraftSpec
}

// BuildBatchResult — состав пакетов и всё, что оператор обязан увидеть до
// решения: digest каждого шарда, исключённые с причинами и число исключённых.
type BuildBatchResult struct {
	CampaignID    domain.CampaignID
	Batches       []BatchComposition
	Excluded      []ExcludedComposition
	Individual    []IndividualComposition
	ExcludedCount int

	// HasMore — окно не покрыло всю очередь: состав не полный, за окном остались
	// ещё черновики. Они не теряются: продолжение доступно по NextCursor.
	HasMore bool

	// NextCursor — курсор продолжения окна; осмыслен при HasMore. Передаётся в
	// BuildBatch.After следующего вызова, поэтому хвост очереди не пропадает.
	NextCursor ports.DraftWindowCursor
}

// BuildBatchHandler — use case формирования состава пакета.
type BuildBatchHandler struct {
	windows ports.DraftWindowRepository
	access  ports.AccessDecision
	risk    domain.CanBeDecidedInPackage
}

// NewBuildBatchHandler собирает сценарий из порта загрузки окна черновиков,
// порта доступа и внешнего предиката риска. nil-предикат означает «рисков нет»
// и трактуется доменом так же; решение об исключении всё равно принимает домен.
//
// Сценарию нужен именно DraftWindowRepository: полная загрузка агрегата в этом
// пути запрещена, иначе кампания на 50 000 черновиков вернётся в память целиком.
func NewBuildBatchHandler(
	windows ports.DraftWindowRepository,
	access ports.AccessDecision,
	risk domain.CanBeDecidedInPackage,
) *BuildBatchHandler {
	return &BuildBatchHandler{windows: windows, access: access, risk: risk}
}

// Handle формирует состав пакетов из окна ожидающих черновиков кампании.
// Проверка доступа выполняется прикладным слоем; однородность, минимальный
// размер и исключение рисковых — доменом.
//
// Граница тенанта восстанавливается явной передачей тенанта команды в запрос
// окна: кампания должна принадлежать cmd.TenantID. Если тенант чужой, окно
// недоступно (порт отвечает ErrCampaignNotFound) и состав не выдаётся — без
// раскрытия существования кампании через отдельную ошибку.
//
// Лимит окна берётся из MaxSize: это максимум черновиков, который
// рассматривает один вызов, то есть лимит окна (см. domain.BatchLimits). Если
// окно не покрыло очередь, результат честно сообщает HasMore и курсор
// продолжения, а не выдаёт часть за полный состав.
func (h *BuildBatchHandler) Handle(ctx context.Context, cmd BuildBatch) (BuildBatchResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return BuildBatchResult{}, err
	}

	window, err := h.windows.LoadDraftWindow(ctx, ports.DraftWindowQuery{
		TenantID:   cmd.TenantID,
		CampaignID: cmd.CampaignID,
		After:      cmd.After,
		Limit:      cmd.Limits.MaxSize,
	})
	if err != nil {
		return BuildBatchResult{}, fmt.Errorf("загрузка окна черновиков кампании %s: %w", cmd.CampaignID, err)
	}

	built, err := domain.BuildApprovalBatchFromDrafts(window.Drafts, cmd.CampaignID, cmd.Limits, h.risk)
	if err != nil {
		return BuildBatchResult{}, err
	}

	result := projectBuildResult(cmd.CampaignID, built)
	result.HasMore = window.HasMore
	result.NextCursor = window.Next
	return result, nil
}

// projectBuildResult сериализует доменный результат. Никакой фильтрации здесь
// нет: состав, исключённые и направленные на индивидуальное рассмотрение берутся
// из результата домена без изменений.
func projectBuildResult(campaignID domain.CampaignID, built domain.BatchBuildResult) BuildBatchResult {
	result := BuildBatchResult{CampaignID: campaignID}
	for _, batch := range built.Batches() {
		result.Batches = append(result.Batches, composeBatch(batch))
	}
	for _, excluded := range built.Excluded() {
		result.Excluded = append(result.Excluded, ExcludedComposition{
			DraftID: excluded.DraftID,
			Version: excluded.Version,
			Reasons: append([]domain.RiskReason(nil), excluded.Reasons...),
		})
	}
	result.ExcludedCount = len(result.Excluded)
	for _, entry := range built.Individual() {
		result.Individual = append(result.Individual, IndividualComposition{
			DraftID: entry.DraftID,
			Version: entry.Version,
			Spec:    entry.Spec,
		})
	}
	return result
}

// composeBatch собирает показ одного шарда: digest — из домена, образец —
// первая позиция, отличия — расхождение остальных позиций с образцом.
func composeBatch(batch domain.ApprovalBatch) BatchComposition {
	entries := batch.Entries()
	composition := BatchComposition{
		Key:     batch.Key(),
		Digest:  batch.Digest(),
		Size:    batch.Size(),
		Entries: make([]BatchCompositionEntry, 0, len(entries)),
	}
	if len(entries) == 0 {
		return composition
	}
	composition.Sample = entries[0].Spec
	for _, entry := range entries {
		composition.Entries = append(composition.Entries, BatchCompositionEntry{
			DraftID: entry.DraftID,
			Version: entry.Version,
			Spec:    entry.Spec,
		})
		composition.Differences = append(composition.Differences, diffFromSample(entry.DraftID, composition.Sample, entry.Spec)...)
	}
	return composition
}

// diffFromSample перечисляет поля позиции, отличающиеся от образца содержимого.
func diffFromSample(draftID domain.DraftID, sample, spec domain.DraftSpec) []BatchDifference {
	var differences []BatchDifference
	if spec.Material != sample.Material {
		differences = append(differences, BatchDifference{DraftID: draftID, Field: "material", From: string(sample.Material), To: string(spec.Material)})
	}
	if spec.Segment != sample.Segment {
		differences = append(differences, BatchDifference{DraftID: draftID, Field: "segment", From: string(sample.Segment), To: string(spec.Segment)})
	}
	if spec.Recipient != sample.Recipient {
		differences = append(differences, BatchDifference{DraftID: draftID, Field: "recipient", From: string(sample.Recipient), To: string(spec.Recipient)})
	}
	if !spec.Channel.Equals(sample.Channel) {
		differences = append(differences, BatchDifference{DraftID: draftID, Field: "channel", From: sample.Channel.String(), To: spec.Channel.String()})
	}
	return differences
}
