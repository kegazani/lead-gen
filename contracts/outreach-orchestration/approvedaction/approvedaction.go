package approvedaction

import (
	"fmt"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Контракт является command intent
// для message-delivery: получатель исполняет утверждённое действие
// идемпотентно и не принимает решения о допустимости.
const ContractName = "approved-action"

// SchemaVersion — текущая версия схемы. Изменять по правилам semver из
// envelope.go.
//
// Версия 1.1.0 добавлена по правилу MINOR: состав, типы и обязательность полей
// не изменились, а идентификатор утверждённого действия стал уникальным в рамках
// кампании-владельца (уточнение области уникальности значения; `approved_action_id`
// остаётся непрозрачной строкой для потребителя).
const SchemaVersion = "1.1.0"

// Имена каналов исполнения. Значения совпадают с доменным VO action-channel
// владельца (internal/domain/action_channel.go): контракт не отвергает и не
// принимает канал, которого нет в домене. Набор закрыт — неизвестный канал в
// контракт не проходит.
const (
	// ChannelEmailName — доставка письма.
	ChannelEmailName = "email"
	// ChannelCallName — звонок.
	ChannelCallName = "call"
	// ChannelFormName — обращение через форму.
	ChannelFormName = "form"
)

var allowedChannels = map[string]struct{}{
	ChannelEmailName: {},
	ChannelCallName:  {},
	ChannelFormName:  {},
}

// parseChannel разбирает имя канала: регистр и окружающие пробелы не значимы,
// неизвестное имя отвергается. Возвращается каноническое (нижний регистр) имя.
func parseChannel(name string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if _, ok := allowedChannels[normalized]; !ok {
		return "", fmt.Errorf("approvedaction: неизвестный канал %q", name)
	}
	return normalized, nil
}

// ApprovedAction — публикуемый контракт утверждённого человеком-оператором
// действия. Публикуется после commit изменения агрегата через outbox.
//
// Контракт передаёт минимум: идентификаторы и непрозрачные ссылки. Адрес
// получателя, имя, тело письма и прочие персональные данные здесь запрещены;
// RecipientRef — непрозрачная ссылка, разрешаемая на стороне получателя.
//
// Ключ идемпотентности — (TenantID, CampaignID, DraftID, DraftVersion): повторная
// доставка того же действия не создаёт второй внешний эффект, а действие другой
// кампании к тому же получателю — не повтор и не сталкивается с ним.
type ApprovedAction struct {
	Envelope

	// ApprovedActionID — идентификатор утверждённого действия. Уникален в рамках
	// кампании-владельца: черновик адресует получателя и повторяется между
	// кампаниями, поэтому идентичность включает кампанию (см.
	// ApprovedActionIdentity). Непрозрачен для потребителя.
	ApprovedActionID string `json:"approved_action_id"`
	// CampaignID — ссылка на кампанию-владельца.
	CampaignID string `json:"campaign_id"`
	// DraftID — ссылка на черновик действия.
	DraftID string `json:"draft_id"`
	// DraftVersion — точная версия черновика, к которой относится решение.
	DraftVersion int64 `json:"draft_version"`
	// DecisionID — ссылка на решение оператора, санкционировавшее действие.
	DecisionID string `json:"decision_id"`
	// Channel — канал исполнения (значение VO action-channel).
	Channel string `json:"channel"`
	// RecipientRef — минимальная непрозрачная ссылка на получателя.
	// Не адрес и не персональные данные.
	RecipientRef string `json:"recipient_ref"`
	// MaterialVersionRef — необязательная ссылка на версию материала.
	MaterialVersionRef string `json:"material_version_ref,omitempty"`
	// DecidedAt — момент принятия решения оператором.
	DecidedAt time.Time `json:"decided_at"`
}

// NewApprovedAction возвращает контракт с проставленными именем и версией
// схемы. Остальные поля заполняет вызывающий.
func NewApprovedAction(env Envelope) ApprovedAction {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return ApprovedAction{Envelope: env}
}

// Validate проверяет envelope и обязательные поля контракта. Канал сверяется с
// закрытым набором каналов, а не с произвольной непустой строкой. Ошибка
// называет все отсутствующие или некорректные поля.
func (a ApprovedAction) Validate() error {
	if err := a.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if a.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(a.ApprovedActionID) == "" {
		invalid = append(invalid, "approved_action_id")
	}
	if strings.TrimSpace(a.CampaignID) == "" {
		invalid = append(invalid, "campaign_id")
	}
	if strings.TrimSpace(a.DraftID) == "" {
		invalid = append(invalid, "draft_id")
	}
	if a.DraftVersion <= 0 {
		invalid = append(invalid, "draft_version")
	}
	if strings.TrimSpace(a.DecisionID) == "" {
		invalid = append(invalid, "decision_id")
	}
	if _, err := parseChannel(a.Channel); err != nil {
		invalid = append(invalid, "channel")
	}
	if strings.TrimSpace(a.RecipientRef) == "" {
		invalid = append(invalid, "recipient_ref")
	}
	if a.DecidedAt.IsZero() {
		invalid = append(invalid, "decided_at")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("approvedaction: approved-action: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Ключ внутренний и
// не является частью wire-формата; компоненты кодируются, чтобы разделитель
// не допускал коллизий.
//
// Кампания входит в ключ: идентичность действия уникальна в рамках кампании
// (черновик адресует получателя и повторяется между кампаниями), поэтому без
// кампании действия разных кампаний к одному получателю дали бы один ключ и
// были бы ошибочно приняты за повтор.
func (a ApprovedAction) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s|%s|%d", encodeKeyPart(a.TenantID), encodeKeyPart(a.CampaignID), encodeKeyPart(a.DraftID), a.DraftVersion)
}

// ApprovedActionIdentity возвращает идентификатор утверждённого действия,
// уникальный в рамках кампании-владельца. Идентичность — (кампания, черновик,
// версия): черновик адресует получателя и потому повторяется между кампаниями к
// одному получателю, поэтому без кампании идентификаторы разных действий
// столкнулись бы. Компоненты кодируются, чтобы разделители ":"/"@" внутри
// значений не допускали коллизий между разными тройками.
func ApprovedActionIdentity(campaignID, draftID string, version int64) string {
	return fmt.Sprintf("%s:%s@%d", encodeKeyPart(campaignID), encodeKeyPart(draftID), version)
}
