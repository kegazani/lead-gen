package httpapi

import (
	"net/http"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// batchDraftView — позиция состава в ответе: черновик, его точная версия и
// содержимое. Версию обязан пережить круговой рейс до решения: пакетное решение
// адресует черновик точной версией (инвариант I2), поэтому адаптер возвращает её
// без изменений и принимает обратно как есть.
type batchDraftView struct {
	DraftID      string `json:"draft_id"`
	DraftVersion int    `json:"draft_version"`
	MaterialID   string `json:"material_id"`
	SegmentID    string `json:"segment_id"`
	RecipientID  string `json:"recipient_id"`
	Channel      string `json:"channel"`
}

// newBatchDraftView переводит позицию состава в форму ответа.
func newBatchDraftView(draftID domain.DraftID, version int, spec domain.DraftSpec) batchDraftView {
	return batchDraftView{
		DraftID:      draftID.String(),
		DraftVersion: version,
		MaterialID:   spec.Material.String(),
		SegmentID:    spec.Segment.String(),
		RecipientID:  spec.Recipient.String(),
		Channel:      spec.Channel.String(),
	}
}

// batchSampleView — образец содержимого пакета: содержимое без привязки к
// конкретному черновику. Отличия остальных позиций показываются относительно
// него.
type batchSampleView struct {
	MaterialID  string `json:"material_id"`
	SegmentID   string `json:"segment_id"`
	RecipientID string `json:"recipient_id"`
	Channel     string `json:"channel"`
}

// batchExcludedView — черновик, исключённый агрегатом, с причинами по каждому
// признаку. Адаптер причины не вычисляет и не пересматривает.
type batchExcludedView struct {
	DraftID      string   `json:"draft_id"`
	DraftVersion int      `json:"draft_version"`
	Reasons      []string `json:"reasons"`
}

// newBatchExcludedView переводит исключённые с причинами в форму ответа.
func newBatchExcludedView(excluded []query.BatchQueueExcluded) []batchExcludedView {
	views := make([]batchExcludedView, 0, len(excluded))
	for _, item := range excluded {
		reasons := make([]string, 0, len(item.Reasons))
		for _, reason := range item.Reasons {
			reasons = append(reasons, string(reason))
		}
		views = append(views, batchExcludedView{
			DraftID:      item.DraftID.String(),
			DraftVersion: item.DraftVersion,
			Reasons:      reasons,
		})
	}
	return views
}

// batchQueueShardView — шард пакетной проекции очереди: основание однородности,
// digest состава и позиции.
type batchQueueShardView struct {
	MaterialID string           `json:"material_id"`
	SegmentID  string           `json:"segment_id"`
	Digest     string           `json:"digest"`
	Size       int              `json:"size"`
	Items      []batchDraftView `json:"items"`
}

// batchQueueResponse — пакетная проекция очереди.
type batchQueueResponse struct {
	CampaignID    string                `json:"campaign_id"`
	Batches       []batchQueueShardView `json:"batches"`
	Excluded      []batchExcludedView   `json:"excluded"`
	ExcludedCount int                   `json:"excluded_count"`
	Individual    []batchDraftView      `json:"individual"`
}

// handleBatchQueue обрабатывает GET /campaigns/{campaignID}/batches. Проекция
// строится use case из результата агрегата; обработчик не группирует и не
// фильтрует черновики. tenant_id и actor_id обязательны и ограничивают выборку
// правом чтения, как в остальных читающих обработках среза.
func (s *Server) handleBatchQueue(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()

	projection, err := s.batchQueue.Handle(r.Context(), query.BatchQueue{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(values.Get("tenant_id")),
		Actor:      domain.ActorID(values.Get("actor_id")),
		Limits:     s.batchLimits,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	response := batchQueueResponse{
		CampaignID: projection.CampaignID.String(),
		Batches:    make([]batchQueueShardView, 0, len(projection.Batches)),
		Individual: make([]batchDraftView, 0, len(projection.Individual)),
	}
	for _, batch := range projection.Batches {
		shard := batchQueueShardView{
			MaterialID: batch.Key.Material.String(),
			SegmentID:  batch.Key.Segment.String(),
			Digest:     batch.Digest,
			Size:       batch.Size,
			Items:      make([]batchDraftView, 0, len(batch.Items)),
		}
		for _, item := range batch.Items {
			shard.Items = append(shard.Items, newBatchDraftView(item.DraftID, item.DraftVersion, item.Spec))
		}
		response.Batches = append(response.Batches, shard)
	}
	response.Excluded = newBatchExcludedView(projection.Excluded)
	response.ExcludedCount = len(projection.Excluded)
	for _, item := range projection.Individual {
		response.Individual = append(response.Individual, newBatchDraftView(item.DraftID, item.DraftVersion, item.Spec))
	}
	writeJSON(w, http.StatusOK, response)
}

// batchDifferenceView — отличие позиции от образца содержимого пакета.
type batchDifferenceView struct {
	DraftID string `json:"draft_id"`
	Field   string `json:"field"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// batchCompositionShardView — показ одного шарда: digest состава, образец
// содержимого, состав с точными версиями и перечень отличий внутри пакета.
type batchCompositionShardView struct {
	MaterialID  string                `json:"material_id"`
	SegmentID   string                `json:"segment_id"`
	Digest      string                `json:"digest"`
	Size        int                   `json:"size"`
	Sample      batchSampleView       `json:"sample"`
	Entries     []batchDraftView      `json:"entries"`
	Differences []batchDifferenceView `json:"differences"`
}

// batchCursorView — курсор продолжения окна: те же доменные идентификаторы,
// которые принимает параметр after_* следующего запроса.
type batchCursorView struct {
	MaterialID string `json:"material_id"`
	SegmentID  string `json:"segment_id"`
	DraftID    string `json:"draft_id"`
}

// batchCompositionResponse — состав пакетов и всё, что оператор обязан увидеть
// до решения: digest каждого шарда, исключённые с причинами и их число,
// черновики на индивидуальное рассмотрение. HasMore честно сообщает, что окно
// не покрыло очередь: часть состава осталась за курсором продолжения, и выдавать
// её за полный состав нельзя.
type batchCompositionResponse struct {
	CampaignID    string                      `json:"campaign_id"`
	Batches       []batchCompositionShardView `json:"batches"`
	Excluded      []batchExcludedView         `json:"excluded"`
	ExcludedCount int                         `json:"excluded_count"`
	Individual    []batchDraftView            `json:"individual"`
	HasMore       bool                        `json:"has_more"`
	NextCursor    *batchCursorView            `json:"next_cursor,omitempty"`
}

// handleBatchComposition обрабатывает GET /campaigns/{campaignID}/batches/composition.
// Состав формирует агрегат; обработчик лишь сериализует его. Полученный здесь
// digest — единственный способ принять пакетное решение: без него use case
// отвергает команду («решение без просмотра», design.md Решение 3).
//
// Параметры after_material, after_segment и after_draft_id задают продолжение
// окна; курсор разбирает порт (ports.DraftWindowQuery.Validate), а не адаптер.
func (s *Server) handleBatchComposition(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()

	result, err := s.buildBatch.Handle(r.Context(), command.BuildBatch{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(values.Get("tenant_id")),
		Actor:      domain.ActorID(values.Get("actor_id")),
		Limits:     s.batchLimits,
		After: ports.DraftWindowCursor{
			Material: domain.MaterialID(values.Get("after_material")),
			Segment:  domain.SegmentID(values.Get("after_segment")),
			DraftID:  domain.DraftID(values.Get("after_draft_id")),
		},
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	response := batchCompositionResponse{
		CampaignID:    result.CampaignID.String(),
		Batches:       make([]batchCompositionShardView, 0, len(result.Batches)),
		Excluded:      make([]batchExcludedView, 0, len(result.Excluded)),
		Individual:    make([]batchDraftView, 0, len(result.Individual)),
		ExcludedCount: result.ExcludedCount,
		HasMore:       result.HasMore,
	}
	for _, batch := range result.Batches {
		shard := batchCompositionShardView{
			MaterialID: batch.Key.Material.String(),
			SegmentID:  batch.Key.Segment.String(),
			Digest:     batch.Digest,
			Size:       batch.Size,
			Sample: batchSampleView{
				MaterialID:  batch.Sample.Material.String(),
				SegmentID:   batch.Sample.Segment.String(),
				RecipientID: batch.Sample.Recipient.String(),
				Channel:     batch.Sample.Channel.String(),
			},
			Entries:     make([]batchDraftView, 0, len(batch.Entries)),
			Differences: make([]batchDifferenceView, 0, len(batch.Differences)),
		}
		for _, entry := range batch.Entries {
			shard.Entries = append(shard.Entries, newBatchDraftView(entry.DraftID, entry.Version, entry.Spec))
		}
		for _, difference := range batch.Differences {
			shard.Differences = append(shard.Differences, batchDifferenceView{
				DraftID: difference.DraftID.String(),
				Field:   difference.Field,
				From:    difference.From,
				To:      difference.To,
			})
		}
		response.Batches = append(response.Batches, shard)
	}
	for _, excluded := range result.Excluded {
		reasons := make([]string, 0, len(excluded.Reasons))
		for _, reason := range excluded.Reasons {
			reasons = append(reasons, string(reason))
		}
		response.Excluded = append(response.Excluded, batchExcludedView{
			DraftID:      excluded.DraftID.String(),
			DraftVersion: excluded.Version,
			Reasons:      reasons,
		})
	}
	for _, item := range result.Individual {
		response.Individual = append(response.Individual, newBatchDraftView(item.DraftID, item.Version, item.Spec))
	}
	if !result.NextCursor.IsZero() {
		response.NextCursor = &batchCursorView{
			MaterialID: result.NextCursor.Material.String(),
			SegmentID:  result.NextCursor.Segment.String(),
			DraftID:    result.NextCursor.DraftID.String(),
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// batchApproveItemRequest — позиция состава из тела команды: черновик и точная
// версия, показанные оператору вместе с digest.
type batchApproveItemRequest struct {
	DraftID      string `json:"draft_id"`
	DraftVersion int    `json:"draft_version"`
}

// batchApproveRequest — тело команды пакетного решения. Digest обязателен: без
// него сценарий отвергает команду, поэтому принять пакет, состав которого не был
// получен, нельзя. Composition повторяет показанный состав с точными версиями;
// источником истины он не является — агрегат пересчитывает digest сам.
type batchApproveRequest struct {
	TenantID    string                    `json:"tenant_id"`
	ActorID     string                    `json:"actor_id"`
	MaterialID  string                    `json:"material_id"`
	SegmentID   string                    `json:"segment_id"`
	Digest      string                    `json:"digest"`
	Composition []batchApproveItemRequest `json:"composition"`
	Reason      string                    `json:"reason"`
}

// batchDecisionView — решение по одной позиции пакета. Пакета целиком в ответе
// нет: решений столько же, сколько черновиков (design.md, Решение 4).
type batchDecisionView struct {
	DecisionID   string `json:"decision_id"`
	DraftID      string `json:"draft_id"`
	DraftVersion int    `json:"draft_version"`
	Batch        bool   `json:"batch"`
}

// batchApproveResponse — итог пакетного решения. Digest лишь называет провенанс
// состава, по которому решения были приняты.
type batchApproveResponse struct {
	CampaignID string              `json:"campaign_id"`
	Digest     string              `json:"digest"`
	Recorded   int                 `json:"recorded"`
	EventCount int                 `json:"event_count"`
	Decisions  []batchDecisionView `json:"decisions"`
}

// handleApproveBatch обрабатывает POST /campaigns/{campaignID}/batches/approve.
//
// Обработчик не проверяет digest и не решает, допустим ли состав: пустой или
// несовпавший digest, неоднородность, изменившаяся версия и признак риска
// отвергаются сценарием и агрегатом. Здесь только разбор тела, вызов use case и
// перевод ошибки в код ответа.
func (s *Server) handleApproveBatch(w http.ResponseWriter, r *http.Request) {
	var req batchApproveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	composition := make([]command.BatchApproveItem, 0, len(req.Composition))
	for _, item := range req.Composition {
		composition = append(composition, command.BatchApproveItem{
			DraftID: domain.DraftID(item.DraftID),
			Version: item.DraftVersion,
		})
	}

	result, err := s.approveBatch.Handle(r.Context(), command.ApproveBatch{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(req.TenantID),
		Actor:      domain.ActorID(req.ActorID),
		Key: domain.HomogeneityKey{
			Material: domain.MaterialID(req.MaterialID),
			Segment:  domain.SegmentID(req.SegmentID),
		},
		Digest:      req.Digest,
		Composition: composition,
		Reason:      req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	response := batchApproveResponse{
		CampaignID: result.CampaignID.String(),
		Digest:     result.Digest,
		Recorded:   result.Recorded,
		EventCount: result.EventCount,
		Decisions:  make([]batchDecisionView, 0, len(result.Decisions)),
	}
	for _, decision := range result.Decisions {
		response.Decisions = append(response.Decisions, batchDecisionView{
			DecisionID:   decision.DecisionID.String(),
			DraftID:      decision.DraftID.String(),
			DraftVersion: decision.DraftVersion,
			Batch:        decision.Batch,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
