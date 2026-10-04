package httpapi

import (
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// machineTimeFormat — формат показа момента в разметке: однозначен независимо
// от языка интерфейса.
const machineTimeFormat = "2006-01-02 15:04"

// draftQueueRow — строка очереди черновиков: черновик, его состояние ожидания и
// результат автоматических проверок (признаки риска).
type draftQueueRow struct {
	CampaignID     string
	DraftID        string
	Version        int
	Channel        string
	MaterialID     string
	SegmentID      string
	RecipientID    string
	RiskReasons    []string
	PostponedUntil string
	ScreenURL      string
}

// Risky сообщает, что по черновику есть признаки риска. Рисковый черновик
// решается только индивидуально и не входит в пакет.
func (r draftQueueRow) Risky() bool { return len(r.RiskReasons) > 0 }

// draftQueueView — модель очереди черновиков с фильтрами.
type draftQueueView struct {
	TenantID       string
	Actor          string
	CSRF           string
	Unavailable    bool
	Notice         string
	CampaignFilter string
	ChannelFilter  string
	RiskFilter     string
	Items          []draftQueueRow
	Stalled        int
	StalledURL     string
}

// batchEntryView — позиция состава.
type batchEntryView struct {
	DraftID     string
	Version     int
	RecipientID string
	Channel     string
	ScreenURL   string
}

// batchDiffRow — отличие позиции от образца пакета.
type batchDiffRow struct {
	DraftID string
	Field   string
	From    string
	To      string
}

// batchShardView — шард пакета: число черновиков, основание однородности, образец
// и отличия.
type batchShardView struct {
	MaterialID      string
	SegmentID       string
	Size            int
	Digest          string
	SampleRecipient string
	SampleChannel   string
	Entries         []batchEntryView
	Differences     []batchDiffRow
}

// batchExcludedRow — исключённый черновик с причинами.
type batchExcludedRow struct {
	DraftID   string
	Version   int
	Reasons   []string
	ScreenURL string
}

// batchScreenView — экран состава пакетов: всё, что оператор обязан увидеть до
// пакетного решения.
type batchScreenView struct {
	TenantID      string
	Actor         string
	CSRF          string
	CampaignID    string
	Notice        string
	Unavailable   bool
	Batches       []batchShardView
	Excluded      []batchExcludedRow
	ExcludedCount int
	Individual    []batchEntryView
	HasMore       bool
}

// draftEditView — запись правки: автор, время, прежний (сгенерированный) и новый
// состав. Хранится отдельно от текущего состояния черновика.
type draftEditView struct {
	Actor         string
	At            string
	FromMaterial  string
	FromSegment   string
	FromRecipient string
	FromChannel   string
	ToMaterial    string
	ToSegment     string
	ToRecipient   string
	ToChannel     string
}

// draftScreenView — модель экрана одного черновика с индивидуальными решениями.
type draftScreenView struct {
	TenantID     string
	Actor        string
	CSRF         string
	CampaignID   string
	DraftID      string
	Version      int
	Channel      string
	MaterialID   string
	SegmentID    string
	RecipientID  string
	RiskReasons  []string
	PostponedFor string
	Edits        []draftEditView
	Notice       string
	Unavailable  bool
}

// stalledRow — строка среза зависших черновиков.
type stalledRow struct {
	CampaignID  string
	DraftID     string
	Version     int
	Channel     string
	MaterialID  string
	SegmentID   string
	RecipientID string
	State       string
}

// stalledView — модель среза зависших черновиков.
type stalledView struct {
	TenantID    string
	Actor       string
	Unavailable bool
	Notify      bool
	Items       []stalledRow
}

// riskReasonLabels — русские подписи объявленных доменом признаков риска.
// Неизвестный признак показывается как есть: молча скрывать его нельзя.
var riskReasonLabels = map[domain.RiskReason]string{
	domain.RiskPersonalDataDetected: "персональные данные",
	domain.RiskAddressUnverified:    "адрес не подтверждён",
	domain.RiskRequiredFactsMissing: "нет обязательных сведений",
	domain.RiskStopRuleTriggered:    "сработало правило остановки",
}

// riskReasonLabel переводит признак риска в подпись для оператора.
func riskReasonLabel(reason domain.RiskReason) string {
	if label, ok := riskReasonLabels[reason]; ok {
		return label
	}
	return string(reason)
}

// riskReasonLabelsOf переводит список признаков в подписи.
func riskReasonLabelsOf(reasons []domain.RiskReason) []string {
	labels := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		labels = append(labels, riskReasonLabel(reason))
	}
	return labels
}

// queueRows переводит проекцию очереди в строки разметки.
func queueRows(items []query.DraftQueueItem) []draftQueueRow {
	rows := make([]draftQueueRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, draftQueueRow{
			CampaignID:     item.CampaignID.String(),
			DraftID:        item.DraftID.String(),
			Version:        item.DraftVersion,
			Channel:        item.Channel.String(),
			MaterialID:     item.Spec.Material.String(),
			SegmentID:      item.Spec.Segment.String(),
			RecipientID:    item.Spec.Recipient.String(),
			RiskReasons:    riskReasonLabelsOf(item.RiskReasons),
			PostponedUntil: formatMoment(item.PostponedUntil),
			ScreenURL:      draftScreenPath(item.CampaignID.String(), item.DraftID.String()),
		})
	}
	return rows
}

// formatMoment форматирует необязательный момент: нулевой даёт пустую строку.
func formatMoment(moment time.Time) string {
	if moment.IsZero() {
		return ""
	}
	return moment.UTC().Format(machineTimeFormat)
}
