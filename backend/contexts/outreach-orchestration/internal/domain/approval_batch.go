package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// RiskReason — объявленный доменом признак риска черновика, при котором
// черновик не может быть решён пакетно и направляется на индивидуальное
// рассмотрение.
//
// Домен не вычисляет признаки сам: они приходят снаружи, от upstream-политик
// data-governance и lead-management, через предикат CanBeDecidedInPackage.
// Значение лишь называет причину, чтобы исключённый черновик не был отброшен
// молча. Перечень ограничен признаками, у которых есть владелец факта (см.
// design.md, Решение 1): стоп-лист и верификация адреса. Признаки без владельца
// не объявляются, чтобы список соответствовал реально получаемым сигналам.
type RiskReason string

const (
	// RiskAddressUnverified — адрес назначения не подтверждён владельцем
	// верификации (lead-management).
	RiskAddressUnverified RiskReason = "address-unverified"

	// RiskStopRuleTriggered — сработало правило остановки: у получателя есть
	// активная запись стоп-листа (data-governance).
	RiskStopRuleTriggered RiskReason = "stop-rule-triggered"
)

// RiskFactsRequest — запрос фактов риска по черновику: тенант, черновик, его
// точная версия и получатель (непрозрачная ссылка, а не адрес — PII в домене
// нет). Тенант входит в запрос, потому что факты принадлежат владельцам и
// ограничены контуром тенанта.
type RiskFactsRequest struct {
	// TenantID — тенант-владелец фактов; владельцы отвечают только в его контуре.
	TenantID TenantID
	// DraftID — черновик, по которому запрашиваются признаки риска.
	DraftID DraftID
	// Version — точная версия черновика (инвариант I2): факты относятся к ней.
	Version int
	// Recipient — получатель черновика (непрозрачная ссылка); по ней владельцы
	// разрешают адрес и факты. Прямого адреса домен не несёт.
	Recipient RecipientID
}

// CanBeDecidedInPackage — внешний поставщик признаков риска черновика.
//
// Предикат не читает состояние домена: факты готовятся вне агрегата (у
// владельцев) и передаются домену. Возврат ошибки означает недоступность фактов,
// а не «рисков нет»: домен не признаёт черновик свободным от риска при ошибке и
// не исключает его самовольно. Пустой результат без ошибки означает, что признаков
// риска нет.
//
// nil-предикат недопустим: это означало бы молчаливое «рисков нет». Сборка обязана
// задать предикат явно (реальный или честно недоступный), иначе домен возвращает
// ErrRiskFactsUnavailable.
type CanBeDecidedInPackage func(ctx context.Context, req RiskFactsRequest) ([]RiskReason, error)

// BatchLimits — лимиты пакетного утверждения, переданные вызывающим.
//
// Домен не читает конфигурацию: значения приходят параметром. MinSize —
// минимальное число однородных черновиков для пакета; ShardSize — максимум
// черновиков в одном шарде (пакете); MaxSize — максимум черновиков, который
// рассматривает один вызов, то есть лимит окна. Остальные ожидающие черновики
// не теряются: они остаются в очереди и попадут в следующий вызов.
type BatchLimits struct {
	MinSize   int
	ShardSize int
	MaxSize   int
}

// validate проверяет лимиты на положительность и порядок.
func (l BatchLimits) validate() error {
	switch {
	case l.MinSize <= 0:
		return fmt.Errorf("%w: минимальный размер %d", ErrInvalidBatchLimits, l.MinSize)
	case l.ShardSize <= 0:
		return fmt.Errorf("%w: размер шарда %d", ErrInvalidBatchLimits, l.ShardSize)
	case l.MaxSize <= 0:
		return fmt.Errorf("%w: максимальный размер %d", ErrInvalidBatchLimits, l.MaxSize)
	case l.MinSize > l.ShardSize:
		return fmt.Errorf(
			"%w: минимальный размер %d больше размера шарда %d",
			ErrInvalidBatchLimits, l.MinSize, l.ShardSize,
		)
	case l.ShardSize > l.MaxSize:
		return fmt.Errorf(
			"%w: размер шарда %d больше максимального размера %d",
			ErrInvalidBatchLimits, l.ShardSize, l.MaxSize,
		)
	default:
		return nil
	}
}

// HomogeneityKey — основание однородности пакета: материал и сегмент точных
// версий. Черновики с разными ключами в один пакет не объединяются.
type HomogeneityKey struct {
	Material MaterialID
	Segment  SegmentID
}

// String возвращает каноническое представление основания однородности.
func (k HomogeneityKey) String() string {
	return string(k.Material) + " + " + string(k.Segment)
}

// BatchEntry — черновик в составе: точная версия и его состав. Версия
// фиксируется на момент формирования состава и защищает инвариант I2.
type BatchEntry struct {
	DraftID DraftID
	Version int
	Spec    DraftSpec
}

// ExcludedDraft — черновик, исключённый из пакета, с причиной исключения.
// Исключённый черновик не выбрасывается молча: он возвращается вызывающему и
// остаётся на индивидуальное рассмотрение.
type ExcludedDraft struct {
	DraftID DraftID
	Version int
	Reasons []RiskReason
}

// ApprovalBatch — один шард состава пакета: однородные черновики в пределах
// лимита. Пакет производен от состояния агрегата и не хранится как запись.
type ApprovalBatch struct {
	key     HomogeneityKey
	entries []BatchEntry
}

// Key возвращает основание однородности пакета.
func (b ApprovalBatch) Key() HomogeneityKey { return b.key }

// Entries возвращает состав пакета в порядке постановки черновиков.
func (b ApprovalBatch) Entries() []BatchEntry {
	return append([]BatchEntry(nil), b.entries...)
}

// Size возвращает число черновиков в пакете.
func (b ApprovalBatch) Size() int { return len(b.entries) }

// Digest возвращает детерминированный digest состава. Он зависит от основания
// однородности и пар (черновик, точная версия), поэтому меняется при изменении
// набора черновиков или любой версии.
func (b ApprovalBatch) Digest() string {
	return batchCompositionDigest(b.key, b.entries)
}

// BatchBuildResult — результат формирования пакетов: готовые шарды,
// исключённые черновики с причинами и черновики, направленные на
// индивидуальное рассмотрение из-за нехватки однородных.
type BatchBuildResult struct {
	batches    []ApprovalBatch
	excluded   []ExcludedDraft
	individual []BatchEntry
}

// Batches возвращает сформированные шарды пакетов.
func (r BatchBuildResult) Batches() []ApprovalBatch {
	return append([]ApprovalBatch(nil), r.batches...)
}

// Excluded возвращает исключённые черновики с причинами.
func (r BatchBuildResult) Excluded() []ExcludedDraft {
	return append([]ExcludedDraft(nil), r.excluded...)
}

// Individual возвращает черновики, направленные на индивидуальное
// рассмотрение: однородных меньше минимального размера, поэтому пакет не
// формируется.
func (r BatchBuildResult) Individual() []BatchEntry {
	return append([]BatchEntry(nil), r.individual...)
}

// BuildApprovalBatch формирует составы пакетов из ожидающих черновиков
// агрегата. Метод лишь собирает черновики и делегирует чистую логику
// BuildApprovalBatchFromDrafts, поэтому состав, который видит оператор через
// окно репозитория, и состав, посчитанный по загруженному агрегату, совпадают.
func (c *Campaign) BuildApprovalBatch(
	ctx context.Context,
	limits BatchLimits,
	canBeDecidedInPackage CanBeDecidedInPackage,
) (BatchBuildResult, error) {
	if c.state == CampaignStateCompleted {
		return BatchBuildResult{}, fmt.Errorf("%w: кампания %s", ErrCampaignCompleted, c.id)
	}
	drafts := make([]*ActionDraft, 0, len(c.order))
	for _, id := range c.order {
		drafts = append(drafts, c.drafts[id])
	}
	return BuildApprovalBatchFromDrafts(ctx, drafts, c.id, c.tenantID, limits, canBeDecidedInPackage)
}

// BuildApprovalBatchFromDrafts формирует составы пакетов из набора черновиков
// одной кампании — чистая функция без мутаций и событий.
//
// Функция не зависит от способа получения черновиков: агрегат передаёт свои
// черновики, прикладной слой — окно, загруженное репозиторием (design.md,
// Решение 6). Для одного и того же набора черновиков обе ветви дают одинаковый
// состав и одинаковый digest.
//
// Черновики группируются по ключу однородности (материал + сегмент, точные
// версии) и нарезаются на шарды по лимитам. Рассматриваются только черновики в
// состоянии «ожидает утверждения»: решённые и просроченные в состав не входят.
// Группа меньше минимального размера пакетом не становится и направляется на
// индивидуальное рассмотрение. При превышении размера шарда формируется
// несколько шардов; сверх лимита MaxSize остальные черновики остаются в
// очереди до следующего вызова.
//
// Признаки риска приходят снаружи через предикат canBeDecidedInPackage:
// черновики с непустыми признаками исключаются из состава и возвращаются с
// причиной по каждому. Предикат получает запрос фактов (тенант, черновик, его
// версия и получатель): недоступность фактов возвращается ошибкой и не
// трактуется как «рисков нет» — при ошибке состав не выдаётся. Функция не читает
// конфигурацию и не обращается к другим контекстам. Черновик, не принадлежащий
// campaignID, отвергается: состав собирается только из черновиков адресуемой
// кампании.
func BuildApprovalBatchFromDrafts(
	ctx context.Context,
	drafts []*ActionDraft,
	campaignID CampaignID,
	tenantID TenantID,
	limits BatchLimits,
	canBeDecidedInPackage CanBeDecidedInPackage,
) (BatchBuildResult, error) {
	if err := limits.validate(); err != nil {
		return BatchBuildResult{}, err
	}

	var result BatchBuildResult
	entriesByKey := make(map[HomogeneityKey][]BatchEntry)
	var keyOrder []HomogeneityKey

	for _, draft := range drafts {
		if draft == nil {
			return BatchBuildResult{}, fmt.Errorf("%w: пустая ссылка на черновик в кампании %s", ErrDraftNotFound, campaignID)
		}
		if draft.CampaignID() != campaignID {
			return BatchBuildResult{}, fmt.Errorf(
				"%w: черновик %s не принадлежит кампании %s",
				ErrDraftNotFound, draft.ID(), campaignID,
			)
		}
		if draft.State() != DraftStatePendingApproval {
			continue
		}
		reasons, err := riskReasons(ctx, canBeDecidedInPackage, RiskFactsRequest{
			TenantID:  tenantID,
			DraftID:   draft.ID(),
			Version:   draft.Version(),
			Recipient: draft.Spec().Recipient,
		})
		if err != nil {
			return BatchBuildResult{}, err
		}
		if len(reasons) > 0 {
			result.excluded = append(result.excluded, ExcludedDraft{
				DraftID: draft.ID(),
				Version: draft.Version(),
				Reasons: reasons,
			})
			continue
		}
		key := HomogeneityKey{
			Material: draft.Spec().Material,
			Segment:  draft.Spec().Segment,
		}
		if _, seen := entriesByKey[key]; !seen {
			keyOrder = append(keyOrder, key)
		}
		entriesByKey[key] = append(entriesByKey[key], BatchEntry{
			DraftID: draft.ID(),
			Version: draft.Version(),
			Spec:    draft.Spec(),
		})
	}

	remaining := limits.MaxSize
	for _, key := range keyOrder {
		entries := entriesByKey[key]
		if len(entries) < limits.MinSize {
			result.individual = append(result.individual, entries...)
			continue
		}
		if remaining < limits.MinSize {
			// Лимит окна исчерпан почти полностью: неполный шард меньше
			// минимума не формируется, черновики остаются в очереди.
			break
		}
		allowed := len(entries)
		if allowed > remaining {
			allowed = remaining
		}
		remaining -= allowed
		result.batches = append(result.batches, shardBatch(key, entries[:allowed], limits, &result.individual)...)
	}
	return result, nil
}

// shardBatch режет однородную группу на шарды по размеру шарда. Хвостовой
// шард меньше минимального размера пакетом не становится и уходит в individual.
func shardBatch(
	key HomogeneityKey,
	entries []BatchEntry,
	limits BatchLimits,
	individual *[]BatchEntry,
) []ApprovalBatch {
	var batches []ApprovalBatch
	for start := 0; start < len(entries); start += limits.ShardSize {
		end := start + limits.ShardSize
		if end > len(entries) {
			end = len(entries)
		}
		chunk := entries[start:end]
		if len(chunk) < limits.MinSize {
			*individual = append(*individual, chunk...)
			continue
		}
		batches = append(batches, ApprovalBatch{
			key:     key,
			entries: append([]BatchEntry(nil), chunk...),
		})
	}
	return batches
}

// riskReasons применяет внешний предикат. Незаданный предикат — недоступность
// фактов (ErrRiskFactsUnavailable), а не «рисков нет»: молчаливое отсутствие
// проверки недопустимо.
func riskReasons(ctx context.Context, can CanBeDecidedInPackage, req RiskFactsRequest) ([]RiskReason, error) {
	if can == nil {
		return nil, fmt.Errorf("%w: предикат признаков риска не задан", ErrRiskFactsUnavailable)
	}
	return can(ctx, req)
}

// batchCompositionDigest детерминированно хеширует основание однородности и
// пары (черновик, точная версия). Канонический порядок — по идентификатору и
// версии, поэтому digest не зависит от порядка обхода.
func batchCompositionDigest(key HomogeneityKey, entries []BatchEntry) string {
	canonical := append([]BatchEntry(nil), entries...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].DraftID != canonical[j].DraftID {
			return canonical[i].DraftID < canonical[j].DraftID
		}
		return canonical[i].Version < canonical[j].Version
	})

	h := sha256.New()
	io.WriteString(h, "material\t")
	io.WriteString(h, string(key.Material))
	io.WriteString(h, "\nsegment\t")
	io.WriteString(h, string(key.Segment))
	io.WriteString(h, "\n")
	for _, e := range canonical {
		io.WriteString(h, "draft\t")
		io.WriteString(h, string(e.DraftID))
		io.WriteString(h, "\t")
		io.WriteString(h, strconv.Itoa(e.Version))
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
