package domain

import (
	"fmt"
	"strings"
	"time"
)

// BatchApprovalItem — позиция состава, утверждаемая пакетно: черновик, его
// точная версия и идентификатор создаваемой записи решения. Одна запись
// решения на пакет целиком не создаётся — только по одной на черновик.
type BatchApprovalItem struct {
	DecisionID   DecisionID
	DraftID      DraftID
	DraftVersion int
}

// BatchApprovalParams — команда пакетного утверждения.
//
// Digest — digest состава, полученный оператором при просмотре. Без digest или
// при его несовпадении решение не принимается («решение без просмотра»). Состав
// адресуется точными версиями: изменившийся после просмотра черновик отвергает
// всё решение без изменения состояния.
type BatchApprovalParams struct {
	Actor       ActorID
	DecidedAt   time.Time
	Digest      string
	Composition []BatchApprovalItem
	Reason      string
}

// ApproveBatch утверждает пакет: создаёт по одному operator-decision на каждый
// черновик состава с признаком пакетного принятия.
//
// Перед записью метод повторно валидирует состав из текущего состояния
// агрегата: точные версии (I2), состояние «ожидает утверждения», однородность
// (материал + сегмент) и признаки риска. Рисковый черновик обойти нельзя:
// повторная попытка включить его в состав отвергается. Digest пересчитывается
// и сверяется с переданным; при несовпадении состояние не меняется.
//
// Идемпотентность — по паре (draft_id, draft_version): повтор той же команды не
// создаёт вторую запись и не эмитит второе событие. Эмитится ровно по одному
// action-approved на каждый впервые утверждённый черновик; отдельного события
// «пакет утверждён» нет.
func (c *Campaign) ApproveBatch(
	params BatchApprovalParams,
	canBeDecidedInPackage CanBeDecidedInPackage,
) ([]OperatorDecision, []DomainEvent, error) {
	if c.state == CampaignStateCompleted {
		return nil, nil, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	if len(params.Composition) == 0 {
		return nil, nil, fmt.Errorf("%w: кампания %s", ErrBatchCompositionEmpty, c.id)
	}
	if strings.TrimSpace(params.Digest) == "" {
		return nil, nil, fmt.Errorf("%w: кампания %s", ErrBatchDigestMismatch, c.id)
	}

	// Первый проход: проверки без изменения состояния. Состав пересобирается из
	// текущего состояния, а не берётся на слово из команды.
	entries := make([]BatchEntry, 0, len(params.Composition))
	seen := make(map[DraftID]struct{}, len(params.Composition))
	for _, item := range params.Composition {
		if err := item.DecisionID.validate(); err != nil {
			return nil, nil, err
		}
		if _, exists := seen[item.DraftID]; exists {
			return nil, nil, fmt.Errorf("%w: черновик %s", ErrDuplicateDraft, item.DraftID)
		}
		seen[item.DraftID] = struct{}{}
		if item.DraftVersion <= 0 {
			return nil, nil, fmt.Errorf(
				"%w: черновик %s версии %d",
				ErrDecisionWithoutVersion, item.DraftID, item.DraftVersion,
			)
		}
		draft, err := c.draft(item.DraftID)
		if err != nil {
			return nil, nil, err
		}
		if err := draft.checkVersion(item.DraftVersion); err != nil {
			return nil, nil, err
		}
		if draft.State() != DraftStatePendingApproval && draft.State() != DraftStateApproved {
			return nil, nil, fmt.Errorf(
				"%w: черновик %s в состоянии %q",
				ErrBatchCompositionChanged, item.DraftID, draft.State(),
			)
		}
		if reasons := riskReasons(canBeDecidedInPackage, draft.ID(), draft.Version()); len(reasons) > 0 {
			return nil, nil, fmt.Errorf(
				"%w: черновик %s (%s)",
				ErrBatchDraftNotEligible, item.DraftID, strings.Join(riskReasonNames(reasons), ", "),
			)
		}
		entries = append(entries, BatchEntry{
			DraftID: draft.ID(),
			Version: draft.Version(),
			Spec:    draft.Spec(),
		})
	}

	key := HomogeneityKey{
		Material: entries[0].Spec.Material,
		Segment:  entries[0].Spec.Segment,
	}
	for _, entry := range entries[1:] {
		if entry.Spec.Material != key.Material || entry.Spec.Segment != key.Segment {
			return nil, nil, fmt.Errorf("%w: кампания %s", ErrBatchNotHomogeneous, c.id)
		}
	}
	if batchCompositionDigest(key, entries) != argsDigest(params.Digest) {
		return nil, nil, fmt.Errorf("%w: кампания %s", ErrBatchDigestMismatch, c.id)
	}

	// Конфликтующие прежние решения выявляются до мутаций: при конфликте не
	// меняется ничего.
	idempotent := make([]bool, len(params.Composition))
	for i, item := range params.Composition {
		existing, ok := c.decisionFor(item.DraftID, item.DraftVersion)
		if !ok {
			continue
		}
		if existing.Outcome() != DecisionApproved ||
			existing.Actor() != params.Actor ||
			existing.Reason() != params.Reason ||
			existing.BatchDigest() != argsDigest(params.Digest) {
			return nil, nil, fmt.Errorf(
				"%w: черновик %s версии %d",
				ErrDecisionConflict, item.DraftID, item.DraftVersion,
			)
		}
		idempotent[i] = true
	}

	decisions := make([]OperatorDecision, 0, len(params.Composition))
	var events []DomainEvent
	approvedNow := 0
	for i, item := range params.Composition {
		if idempotent[i] {
			existing, _ := c.decisionFor(item.DraftID, item.DraftVersion)
			decisions = append(decisions, existing)
			continue
		}
		decision, emitted, err := c.commitDecision(
			DecisionParams{
				DecisionID:   item.DecisionID,
				DraftID:      item.DraftID,
				DraftVersion: item.DraftVersion,
				Actor:        params.Actor,
				Reason:       params.Reason,
				DecidedAt:    params.DecidedAt,
			},
			DecisionApproved,
			argsDigest(params.Digest),
		)
		if err != nil {
			return nil, nil, err
		}
		decisions = append(decisions, decision)
		events = append(events, emitted...)
		approvedNow++
	}
	if approvedNow > 0 {
		// Пакетное утверждение изменяет агрегат (его решения), поэтому вместе с
		// N × action-approved фиксируется campaign-state-changed. Состояние при
		// этом остаётся прежним: пакет не вводит нового состояния кампании.
		events = append(events, c.recordBatchStateChange(params.DecidedAt))
	}
	return decisions, events, nil
}

// argsDigest нормализует переданный digest: пробелы не значимы.
func argsDigest(digest string) string { return strings.TrimSpace(digest) }

// riskReasonNames возвращает причины исключения в виде строк для сообщения.
func riskReasonNames(reasons []RiskReason) []string {
	names := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		names = append(names, string(reason))
	}
	return names
}
