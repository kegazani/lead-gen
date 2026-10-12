package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// whenFormat — макет значения поля datetime-local: время отложения решения.
const whenFormat = "2006-01-02T15:04"

// handleDraftQueue обслуживает GET /outreach/drafts: очередь черновиков с
// фильтрами и результатом автоматических проверок риска.
func (s *Server) handleDraftQueue(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	tenant := values.Get("tenant_id")
	actor := actorOf(r)

	view := draftQueueView{
		Live:           consoleLive,
		TenantID:       tenant,
		Actor:          actor,
		CSRF:           csrfOf(r),
		CampaignFilter: values.Get("campaign_id"),
		ChannelFilter:  values.Get("channel"),
		RiskFilter:     values.Get("risk"),
		StalledURL:     outreachURL("/outreach/stalled", tenant),
	}
	// Уведомление о зависших черновиках (срез 4.5): оператор видит, что есть
	// черновики с истёкшим сроком, по которым действие не исполнено.
	if s.deps.StalledDrafts != nil {
		if stalled, err := s.deps.StalledDrafts.Handle(r.Context(), query.StalledDrafts{
			TenantID: domain.TenantID(tenant),
			Actor:    domain.ActorID(actor),
		}); err == nil {
			view.Stalled = len(stalled.Items)
		}
	}

	if s.deps.DraftQueue == nil {
		view.Unavailable = true
		s.render(w, http.StatusOK, pageDraftQueue, components.Page{
			Layout:  components.Layout{Title: "Очередь черновиков"},
			Sidebar: s.consoleNav(tenant, actor),
			Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
			View:    view,
		})
		return
	}

	var channel domain.ActionChannel
	if raw := values.Get("channel"); raw != "" {
		parsed, err := domain.ParseActionChannel(raw)
		if err != nil {
			s.renderPlainError(w, http.StatusBadRequest, "неизвестный канал: "+raw)
			return
		}
		channel = parsed
	}

	items, err := s.deps.DraftQueue.Handle(r.Context(), query.DraftQueue{
		CampaignID: domain.CampaignID(values.Get("campaign_id")),
		Channel:    channel,
		Risk:       query.RiskFilter(values.Get("risk")),
		TenantID:   domain.TenantID(tenant),
		Actor:      domain.ActorID(actor),
	})
	if err != nil {
		s.renderPlainError(w, statusForError(err), "очередь черновиков недоступна")
		return
	}
	view.Items = queueRows(items)
	s.render(w, http.StatusOK, pageDraftQueue, components.Page{
		Layout:  components.Layout{Title: "Очередь черновиков"},
		Sidebar: s.consoleNav(tenant, actor),
		Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
		View:    view,
	})
}

// handleBatchScreen обслуживает GET .../batches: показ состава пакетов до
// решения. Оператор обязан увидеть число черновиков, основание однородности,
// образец, отличия и исключённых с причинами.
func (s *Server) handleBatchScreen(w http.ResponseWriter, r *http.Request) {
	s.renderBatchScreen(w, r, http.StatusOK, r.URL.Query().Get("tenant_id"), actorOf(r), "")
}

// renderBatchScreen собирает модель состава и рисует экран.
func (s *Server) renderBatchScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	campaignID := r.PathValue("campaignID")
	view := batchScreenView{Live: consoleLive, TenantID: tenant, Actor: actor, CSRF: csrfOf(r), CampaignID: campaignID, Notice: notice}
	if s.deps.BuildBatch == nil {
		view.Unavailable = true
		s.render(w, status, pageBatchScreen, components.Page{
			Layout:  components.Layout{Title: "Состав пакетов " + campaignID},
			Sidebar: s.consoleNav(tenant, actor),
			Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
			View:    view,
		})
		return
	}
	result, err := s.deps.BuildBatch.Handle(r.Context(), command.BuildBatch{
		CampaignID: domain.CampaignID(campaignID),
		TenantID:   domain.TenantID(tenant),
		Actor:      domain.ActorID(actor),
		Limits:     s.batchLimits,
		After: ports.DraftWindowCursor{
			Material: domain.MaterialID(r.URL.Query().Get("after_material")),
			Segment:  domain.SegmentID(r.URL.Query().Get("after_segment")),
			DraftID:  domain.DraftID(r.URL.Query().Get("after_draft_id")),
		},
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	view.Batches = batchShards(campaignID, result.Batches)
	view.Excluded = batchExcluded(campaignID, result.Excluded)
	view.ExcludedCount = result.ExcludedCount
	view.Individual = batchEntries(campaignID, result.Individual)
	view.HasMore = result.HasMore
	s.render(w, status, pageBatchScreen, components.Page{
		Layout:  components.Layout{Title: "Состав пакетов " + campaignID},
		Sidebar: s.consoleNav(tenant, actor),
		Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
		View:    view,
	})
}

// handleBatchApprove обслуживает POST .../batches/approve: пакетное решение.
// Без предъявленного состава (digest и позиций) решение отвергается сценарием.
func (s *Server) handleBatchApprove(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := r.FormValue("tenant_id")
	actor := actorOf(r)
	campaignID := r.PathValue("campaignID")

	ids := r.Form["draft_id"]
	versions := r.Form["draft_version"]
	if len(ids) != len(versions) {
		s.renderBatchScreen(w, r, http.StatusBadRequest, tenant, actor, "предъявление состава повреждено")
		return
	}
	composition := make([]command.BatchApproveItem, 0, len(ids))
	for i, id := range ids {
		version, err := strconv.Atoi(versions[i])
		if err != nil {
			s.renderBatchScreen(w, r, http.StatusBadRequest, tenant, actor, "неверная версия черновика")
			return
		}
		composition = append(composition, command.BatchApproveItem{DraftID: domain.DraftID(id), Version: version})
	}

	_, err := s.deps.ApproveBatch.Handle(r.Context(), command.ApproveBatch{
		CampaignID: domain.CampaignID(campaignID),
		TenantID:   domain.TenantID(tenant),
		Actor:      domain.ActorID(actor),
		Key: domain.HomogeneityKey{
			Material: domain.MaterialID(r.FormValue("material_id")),
			Segment:  domain.SegmentID(r.FormValue("segment_id")),
		},
		Digest:      r.FormValue("digest"),
		Composition: composition,
		Reason:      r.FormValue("reason"),
	})
	if err != nil {
		s.renderBatchScreen(w, r, statusForError(err), tenant, actor, err.Error())
		return
	}
	http.Redirect(w, r, outreachURL(batchScreenPath(campaignID), tenant), http.StatusSeeOther)
}

// handleDraftScreen обслуживает GET .../drafts/{draftID}: экран индивидуального
// решения по черновику.
func (s *Server) handleDraftScreen(w http.ResponseWriter, r *http.Request) {
	s.renderDraftScreen(w, r, http.StatusOK, r.URL.Query().Get("tenant_id"), actorOf(r), "")
}

// renderDraftScreen загружает черновик из очереди и рисует экран.
func (s *Server) renderDraftScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	campaignID := r.PathValue("campaignID")
	draftID := r.PathValue("draftID")
	view := draftScreenView{
		Live:     consoleLive,
		TenantID: tenant, Actor: actor, CSRF: csrfOf(r),
		CampaignID: campaignID, DraftID: draftID, Notice: notice,
	}
	if s.deps.DraftQueue == nil {
		view.Unavailable = true
		s.render(w, status, pageDraftScreen, components.Page{
			Layout:  components.Layout{Title: "Черновик " + draftID},
			Sidebar: s.consoleNav(tenant, actor),
			Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
			View:    view,
		})
		return
	}
	item, err := s.loadDraftItem(r, tenant, actor, campaignID, draftID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	view.Version = item.DraftVersion
	view.Channel = item.Channel.String()
	view.MaterialID = item.Spec.Material.String()
	view.SegmentID = item.Spec.Segment.String()
	view.RecipientID = item.Spec.Recipient.String()
	view.RiskReasons = riskReasonLabelsOf(item.RiskReasons)
	if !item.PostponedUntil.IsZero() {
		view.PostponedFor = formatMoment(item.PostponedUntil)
	}
	s.render(w, status, pageDraftScreen, components.Page{
		Layout:  components.Layout{Title: "Черновик " + draftID},
		Sidebar: s.consoleNav(tenant, actor),
		Subnav:  outreachSubnav(consoleNavDrafts, tenant, actor),
		View:    view,
	})
}

// handleDraftApprove обслуживает POST .../approve: утвердить черновик.
func (s *Server) handleDraftApprove(w http.ResponseWriter, r *http.Request) {
	s.runDraftDecision(w, r, func(req *http.Request, item query.DraftQueueItem, actor string) error {
		_, err := s.deps.ApproveDraft.Handle(req.Context(), command.ApproveDraft{
			CampaignID:      domain.CampaignID(req.PathValue("campaignID")),
			TenantID:        domain.TenantID(req.FormValue("tenant_id")),
			Actor:           domain.ActorID(actor),
			DraftID:         item.DraftID,
			DecisionID:      consoleDecisionID(item.CampaignID.String(), item.DraftID.String(), item.DraftVersion, "approve"),
			ExpectedVersion: item.DraftVersion,
			Reason:          req.FormValue("reason"),
		})
		return err
	})
}

// handleDraftEditApprove обслуживает POST .../edit-approve: отредактировать и
// утвердить. Правка хранится отдельно от сгенерированного варианта (автор,
// время, прежний и новый состав), затем утверждается новая версия.
func (s *Server) handleDraftEditApprove(w http.ResponseWriter, r *http.Request) {
	s.runDraftDecision(w, r, func(req *http.Request, item query.DraftQueueItem, actor string) error {
		spec, err := draftSpecFromForm(req)
		if err != nil {
			return err
		}
		revised, err := s.deps.ReviseDraft.Handle(req.Context(), command.ReviseDraft{
			CampaignID:      domain.CampaignID(req.PathValue("campaignID")),
			TenantID:        domain.TenantID(req.FormValue("tenant_id")),
			Actor:           domain.ActorID(actor),
			DraftID:         item.DraftID,
			ExpectedVersion: item.DraftVersion,
			Spec:            spec,
		})
		if err != nil {
			return err
		}
		_, err = s.deps.ApproveDraft.Handle(req.Context(), command.ApproveDraft{
			CampaignID:      domain.CampaignID(req.PathValue("campaignID")),
			TenantID:        domain.TenantID(req.FormValue("tenant_id")),
			Actor:           domain.ActorID(actor),
			DraftID:         item.DraftID,
			DecisionID:      consoleDecisionID(item.CampaignID.String(), item.DraftID.String(), revised.Version, "approve"),
			ExpectedVersion: revised.Version,
			Reason:          req.FormValue("reason"),
		})
		return err
	})
}

// handleDraftReject обслуживает POST .../reject: отклонить черновик с причиной.
func (s *Server) handleDraftReject(w http.ResponseWriter, r *http.Request) {
	s.runDraftDecision(w, r, func(req *http.Request, item query.DraftQueueItem, actor string) error {
		_, err := s.deps.RejectDraft.Handle(req.Context(), command.RejectDraft{
			CampaignID:      domain.CampaignID(req.PathValue("campaignID")),
			TenantID:        domain.TenantID(req.FormValue("tenant_id")),
			Actor:           domain.ActorID(actor),
			DraftID:         item.DraftID,
			DecisionID:      consoleDecisionID(item.CampaignID.String(), item.DraftID.String(), item.DraftVersion, "reject"),
			ExpectedVersion: item.DraftVersion,
			Reason:          req.FormValue("reason"),
		})
		return err
	})
}

// handleDraftPostpone обслуживает POST .../postpone: отложить решение до срока.
// Отложение не является решением и не исполняет действие.
func (s *Server) handleDraftPostpone(w http.ResponseWriter, r *http.Request) {
	s.runDraftDecision(w, r, func(req *http.Request, item query.DraftQueueItem, actor string) error {
		until, err := parseWhen(req.FormValue("until"))
		if err != nil {
			return err
		}
		_, err = s.deps.PostponeDraft.Handle(req.Context(), command.PostponeDraft{
			CampaignID:      domain.CampaignID(req.PathValue("campaignID")),
			TenantID:        domain.TenantID(req.FormValue("tenant_id")),
			Actor:           domain.ActorID(actor),
			DraftID:         item.DraftID,
			ExpectedVersion: item.DraftVersion,
			Until:           until,
		})
		return err
	})
}

// runDraftDecision — общий путь индивидуального решения: разбор формы, загрузка
// черновика, действие и POST/redirect/GET на очередь. Любое индивидуальное
// решение уводит черновик из очереди ожидания (отложение — до срока), поэтому
// оператор возвращается к очереди.
func (s *Server) runDraftDecision(w http.ResponseWriter, r *http.Request, action func(*http.Request, query.DraftQueueItem, string) error) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := r.FormValue("tenant_id")
	actor := actorOf(r)
	item, err := s.loadDraftItem(r, tenant, actor, r.PathValue("campaignID"), r.PathValue("draftID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := action(r, item, actor); err != nil {
		s.renderDraftScreen(w, r, statusForError(err), tenant, actor, err.Error())
		return
	}
	http.Redirect(w, r, outreachURL("/outreach/drafts", tenant), http.StatusSeeOther)
}

// loadDraftItem находит черновик в очереди. Отсутствие черновика (в том числе
// потому, что решение уже принято) — честный исход «черновик не найден».
func (s *Server) loadDraftItem(r *http.Request, tenant, actor, campaignID, draftID string) (query.DraftQueueItem, error) {
	items, err := s.deps.DraftQueue.Handle(r.Context(), query.DraftQueue{
		CampaignID: domain.CampaignID(campaignID),
		TenantID:   domain.TenantID(tenant),
		Actor:      domain.ActorID(actor),
	})
	if err != nil {
		return query.DraftQueueItem{}, err
	}
	for _, item := range items {
		if item.DraftID == domain.DraftID(draftID) {
			return item, nil
		}
	}
	return query.DraftQueueItem{}, domain.ErrDraftNotFound
}

// handleStalledQueue обслуживает GET /outreach/stalled: срез зависших черновиков
// без автоутверждения. Оператор уведомляется, что по истёкшему сроку действие
// осталось неисполненным.
func (s *Server) handleStalledQueue(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := actorOf(r)
	view := stalledView{Live: consoleLive, TenantID: tenant, Actor: actor}
	if s.deps.StalledDrafts == nil {
		view.Unavailable = true
	} else {
		result, err := s.deps.StalledDrafts.Handle(r.Context(), query.StalledDrafts{
			CampaignID: domain.CampaignID(r.URL.Query().Get("campaign_id")),
			TenantID:   domain.TenantID(tenant),
			Actor:      domain.ActorID(actor),
		})
		if err != nil {
			s.renderPlainError(w, statusForError(err), "срез зависших черновиков недоступен")
			return
		}
		view.Notify = result.Notify
		view.Items = stalledRows(result.Items)
	}
	s.render(w, http.StatusOK, pageStalledQueue, components.Page{
		Layout:  components.Layout{Title: "Зависшие черновики"},
		Sidebar: s.consoleNav(tenant, actor),
		Subnav:  outreachSubnav(consoleNavStalled, tenant, actor),
		View:    view,
	})
}

// batchShards переводит шарды состава в модель разметки.
func batchShards(campaignID string, batches []command.BatchComposition) []batchShardView {
	shards := make([]batchShardView, 0, len(batches))
	for _, batch := range batches {
		shard := batchShardView{
			MaterialID:      batch.Key.Material.String(),
			SegmentID:       batch.Key.Segment.String(),
			Size:            batch.Size,
			Digest:          batch.Digest,
			SampleRecipient: batch.Sample.Recipient.String(),
			SampleChannel:   batch.Sample.Channel.String(),
			Entries:         make([]batchEntryView, 0, len(batch.Entries)),
			Differences:     make([]batchDiffRow, 0, len(batch.Differences)),
		}
		for _, entry := range batch.Entries {
			shard.Entries = append(shard.Entries, batchEntryView{
				DraftID:     entry.DraftID.String(),
				Version:     entry.Version,
				RecipientID: entry.Spec.Recipient.String(),
				Channel:     entry.Spec.Channel.String(),
				ScreenURL:   draftScreenPath(campaignID, entry.DraftID.String()),
			})
		}
		for _, difference := range batch.Differences {
			shard.Differences = append(shard.Differences, batchDiffRow{
				DraftID: difference.DraftID.String(),
				Field:   difference.Field,
				From:    difference.From,
				To:      difference.To,
			})
		}
		shards = append(shards, shard)
	}
	return shards
}

// batchEntries переводит позиции состава в модель разметки.
func batchEntries(campaignID string, entries []command.IndividualComposition) []batchEntryView {
	views := make([]batchEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, batchEntryView{
			DraftID:     entry.DraftID.String(),
			Version:     entry.Version,
			RecipientID: entry.Spec.Recipient.String(),
			Channel:     entry.Spec.Channel.String(),
			ScreenURL:   draftScreenPath(campaignID, entry.DraftID.String()),
		})
	}
	return views
}

// batchExcluded переводит исключённые черновики в модель разметки с подписями
// причин.
func batchExcluded(campaignID string, excluded []command.ExcludedComposition) []batchExcludedRow {
	views := make([]batchExcludedRow, 0, len(excluded))
	for _, item := range excluded {
		views = append(views, batchExcludedRow{
			DraftID:   item.DraftID.String(),
			Version:   item.Version,
			Reasons:   riskReasonLabelsOf(item.Reasons),
			ScreenURL: draftScreenPath(campaignID, item.DraftID.String()),
		})
	}
	return views
}

// stalledRows переводит срез зависших черновиков в модель разметки.
func stalledRows(items []query.StalledDraftItem) []stalledRow {
	rows := make([]stalledRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, stalledRow{
			CampaignID:  item.CampaignID.String(),
			DraftID:     item.DraftID.String(),
			Version:     item.DraftVersion,
			Channel:     item.Channel.String(),
			MaterialID:  item.Spec.Material.String(),
			SegmentID:   item.Spec.Segment.String(),
			RecipientID: item.Spec.Recipient.String(),
			State:       string(item.State),
		})
	}
	return rows
}

// consoleDecisionID строит детерминированный идентификатор решения консольного
// пути из кампании, черновика, версии и действия. Детерминированность сохраняет
// идемпотентность повтора формы и не даёт двум решениям одного черновика
// получить один идентификатор при разных исходах.
func consoleDecisionID(campaignID, draftID string, version int, action string) domain.DecisionID {
	sum := sha256.Sum256([]byte(campaignID + "|" + draftID + "|" + strconv.Itoa(version) + "|" + action))
	return domain.DecisionID("console-" + hex.EncodeToString(sum[:8]) + "-" + draftID)
}

// draftSpecFromForm собирает состав правки черновика из формы.
func draftSpecFromForm(r *http.Request) (domain.DraftSpec, error) {
	channel, err := domain.ParseActionChannel(r.FormValue("channel"))
	if err != nil {
		return domain.DraftSpec{}, err
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID(r.FormValue("material_id")),
		Segment:   domain.SegmentID(r.FormValue("segment_id")),
		Recipient: domain.RecipientID(r.FormValue("recipient_id")),
		Channel:   channel,
	}, nil
}

// parseWhen разбирает значение поля datetime-local в момент отложения.
// Пустое значение отвергается доменом как отсутствие срока.
func parseWhen(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("срок отложения не задан")
	}
	return time.ParseInLocation(whenFormat, value, time.UTC)
}

// csrfOf возвращает CSRF-токен сессии для форм экрана.
func csrfOf(r *http.Request) string { return consoleauth.CSRF(r) }
