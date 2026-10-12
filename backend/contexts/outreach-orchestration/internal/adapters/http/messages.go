package httpapi

import "lead-gen/frontend/components"

// Каталог сообщений консоли рассылок (web-frontend-architecture, срез 7, 7.1;
// design.md, Решение 12). Пользовательский текст шаблонов живёт здесь, а не в
// разметке: шаблон ссылается на ключ через функцию text. Базовые ключи (навигация,
// статусы верификации) приходят из общего слоя и не дублируются.
var outreachCatalog = map[components.MessageKey]string{
	// Вход.
	msgLoginTitle:  "Вход в консоль рассылок",
	msgLoginUser:   "Имя пользователя",
	msgLoginPass:   "Пароль",
	msgLoginSubmit: "Войти",
	msgLoginError:  "Неверный логин или пароль.",

	// Общие подписи очередей.
	msgFieldCampaign:  "Кампания",
	msgFieldDraft:     "Черновик",
	msgFieldVersion:   "Версия",
	msgFieldChannel:   "Канал",
	msgFieldRecipient: "Получатель",
	msgFieldState:     "Состояние",
	msgOpen:           "Открыть",
	msgIndividual:     "индивидуально",
	msgRiskFlag:       "рисковый",
	msgRiskFree:       "без риска",
	msgPostponedUntil: "отложен до",

	// Очередь черновиков.
	msgDraftQueueTitle:      "Очередь черновиков",
	msgDraftQueueHint:       "Черновики, ожидающие решения оператора. Тенант:",
	msgDraftQueueStalled:    "Зависших черновиков без автоутверждения:",
	msgDraftQueueStalledURL: "Открыть срез",
	msgDraftQueueUnavail:    "Очередь недоступна: хранилище не подключено",
	msgDraftQueueAny:        "любой",
	msgDraftQueueChecks:     "Проверки",
	msgDraftQueueAll:        "все",
	msgDraftQueueRisky:      "рисковые",
	msgDraftQueueSafe:       "без риска",
	msgDraftQueueApply:      "Применить",
	msgDraftQueueChecksHead: "Автоматические проверки",
	msgDraftQueueRiskyTitle: "Только индивидуальное решение",
	msgDraftQueueEmpty:      "Черновиков, ожидающих решения, нет",

	// Экран черновика.
	msgDraftTitlePrefix:   "Черновик",
	msgDraftCampaign:      "Кампания:",
	msgDraftVersion:       "Версия:",
	msgDraftChannel:       "Канал:",
	msgDraftComposition:   "Состав:",
	msgDraftMaterial:      "материал",
	msgDraftSegment:       "сегмент",
	msgDraftRecipient:     "получатель",
	msgDraftRiskBlock:     "Рисковый черновик — решение только индивидуальное, в пакет он не входит.",
	msgDraftPostponed:     "Решение отложено до",
	msgDraftUnavail:       "Черновик недоступен: хранилище не подключено",
	msgDraftEditsTitle:    "Правки (хранятся отдельно от сгенерированного)",
	msgDraftApprove:       "Утвердить",
	msgDraftMaterialLabel: "Материал",
	msgDraftSegmentLabel:  "Сегмент",
	msgDraftRecipientLbl:  "Получатель",
	msgDraftChannelLabel:  "Канал",
	msgDraftEditApprove:   "Отредактировать и утвердить",
	msgDraftReason:        "Причина",
	msgDraftReject:        "Отклонить",
	msgDraftPostponeUntil: "Отложить до",
	msgDraftPostpone:      "Отложить",

	// Состав пакетов.
	msgBatchTitlePrefix:   "Состав пакетов кампании",
	msgBatchUnavail:       "Состав недоступен: хранилище не подключено",
	msgBatchPacket:        "Пакет:",
	msgBatchSize:          "Число черновиков в пакете:",
	msgBatchHomogeneity:   "Основание однородности: материал",
	msgBatchSegment:       "сегмент",
	msgBatchSample:        "Образец: получатель",
	msgBatchChannel:       "канал",
	msgBatchDigest:        "Digest состава:",
	msgBatchDifferences:   "Отличия от образца",
	msgBatchReason:        "Причина (необязательно)",
	msgBatchApprove:       "Утвердить пакет",
	msgBatchEmpty:         "Пакетов нет: однородных черновиков меньше минимального размера.",
	msgBatchExcluded:      "Исключено из пакета:",
	msgBatchExcludedEmpty: "Исключённых черновиков нет.",
	msgBatchIndividual:    "На индивидуальное рассмотрение",
	msgBatchIndividualURL: "открыть",
	msgBatchMore:          "Окно не покрыло всю очередь: показана часть состава.",

	// Зависшие черновики.
	msgStalledTitle:      "Зависшие черновики",
	msgStalledHint:       "Черновики с истёкшим сроком утверждения. Автоутверждения нет — действие не исполнено. Тенант:",
	msgStalledUnavail:    "Срез недоступен: хранилище не подключено",
	msgStalledNotify:     "Оператор уведомляется: зависших черновиков",
	msgStalledNotifyTail: "Срок истёк, но действие осталось неисполненным.",
	msgStalledEmpty:      "Зависших черновиков нет",

	msgEditorialTitle:          "Редакторская проверка",
	msgEditorialWarning:        "Одобрение текста не разрешает отправку. Решение фиксируется только для показанной версии.",
	msgEditorialCampaign:       "Кампания",
	msgEditorialLead:           "Лид",
	msgEditorialMaterial:       "Материал",
	msgEditorialVersion:        "Версия",
	msgEditorialSubject:        "Тема",
	msgEditorialBody:           "Текст",
	msgEditorialReason:         "Причина отклонения (обязательна для reject)",
	msgEditorialApprove:        "Одобрить текст",
	msgEditorialReject:         "Отклонить текст",
	msgEditorialRecorded:       "Решение сохранено",
	msgEditorialReasonRecorded: "Причина",

	// Читаемые подписи состояний черновика (change console-liquid-glass):
	// шаблон берёт их функцией label, неизвестное состояние показывается как есть.
	"outreach.draft.state.proposed":         "Предложен",
	"outreach.draft.state.pending-approval": "Ждёт утверждения",
	"outreach.draft.state.approved":         "Утверждён",
	"outreach.draft.state.rejected":         "Отклонён",
	"outreach.draft.state.expired":          "Истёк срок",
	"outreach.draft.state.executed":         "Отправлен",
	"outreach.draft.state.failed":           "Сбой",
}

// Ключи каталога консоли рассылок.
const (
	msgLoginTitle  components.MessageKey = "outreach.login.title"
	msgLoginUser   components.MessageKey = "outreach.login.username"
	msgLoginPass   components.MessageKey = "outreach.login.password"
	msgLoginSubmit components.MessageKey = "outreach.login.submit"
	msgLoginError  components.MessageKey = "outreach.login.error"

	msgFieldCampaign  components.MessageKey = "outreach.field.campaign"
	msgFieldDraft     components.MessageKey = "outreach.field.draft"
	msgFieldVersion   components.MessageKey = "outreach.field.version"
	msgFieldChannel   components.MessageKey = "outreach.field.channel"
	msgFieldRecipient components.MessageKey = "outreach.field.recipient"
	msgFieldState     components.MessageKey = "outreach.field.state"
	msgOpen           components.MessageKey = "outreach.action.open"
	msgIndividual     components.MessageKey = "outreach.action.individual"
	msgRiskFlag       components.MessageKey = "outreach.risk.flag"
	msgRiskFree       components.MessageKey = "outreach.risk.free"
	msgPostponedUntil components.MessageKey = "outreach.risk.postponed_until"

	msgDraftQueueTitle      components.MessageKey = "outreach.draft_queue.title"
	msgDraftQueueHint       components.MessageKey = "outreach.draft_queue.hint"
	msgDraftQueueStalled    components.MessageKey = "outreach.draft_queue.stalled"
	msgDraftQueueStalledURL components.MessageKey = "outreach.draft_queue.stalled_url"
	msgDraftQueueUnavail    components.MessageKey = "outreach.draft_queue.unavailable"
	msgDraftQueueAny        components.MessageKey = "outreach.draft_queue.any"
	msgDraftQueueChecks     components.MessageKey = "outreach.draft_queue.checks"
	msgDraftQueueAll        components.MessageKey = "outreach.draft_queue.all"
	msgDraftQueueRisky      components.MessageKey = "outreach.draft_queue.risky"
	msgDraftQueueSafe       components.MessageKey = "outreach.draft_queue.safe"
	msgDraftQueueApply      components.MessageKey = "outreach.draft_queue.apply"
	msgDraftQueueChecksHead components.MessageKey = "outreach.draft_queue.checks_header"
	msgDraftQueueRiskyTitle components.MessageKey = "outreach.draft_queue.risky_title"
	msgDraftQueueEmpty      components.MessageKey = "outreach.draft_queue.empty"

	msgDraftTitlePrefix   components.MessageKey = "outreach.draft.title"
	msgDraftCampaign      components.MessageKey = "outreach.draft.campaign"
	msgDraftVersion       components.MessageKey = "outreach.draft.version"
	msgDraftChannel       components.MessageKey = "outreach.draft.channel"
	msgDraftComposition   components.MessageKey = "outreach.draft.composition"
	msgDraftMaterial      components.MessageKey = "outreach.draft.material"
	msgDraftSegment       components.MessageKey = "outreach.draft.segment"
	msgDraftRecipient     components.MessageKey = "outreach.draft.recipient"
	msgDraftRiskBlock     components.MessageKey = "outreach.draft.risk_block"
	msgDraftPostponed     components.MessageKey = "outreach.draft.postponed"
	msgDraftUnavail       components.MessageKey = "outreach.draft.unavailable"
	msgDraftEditsTitle    components.MessageKey = "outreach.draft.edits_title"
	msgDraftApprove       components.MessageKey = "outreach.draft.approve"
	msgDraftMaterialLabel components.MessageKey = "outreach.draft.material_label"
	msgDraftSegmentLabel  components.MessageKey = "outreach.draft.segment_label"
	msgDraftRecipientLbl  components.MessageKey = "outreach.draft.recipient_label"
	msgDraftChannelLabel  components.MessageKey = "outreach.draft.channel_label"
	msgDraftEditApprove   components.MessageKey = "outreach.draft.edit_approve"
	msgDraftReason        components.MessageKey = "outreach.draft.reason"
	msgDraftReject        components.MessageKey = "outreach.draft.reject"
	msgDraftPostponeUntil components.MessageKey = "outreach.draft.postpone_until"
	msgDraftPostpone      components.MessageKey = "outreach.draft.postpone"

	msgBatchTitlePrefix   components.MessageKey = "outreach.batch.title"
	msgBatchUnavail       components.MessageKey = "outreach.batch.unavailable"
	msgBatchPacket        components.MessageKey = "outreach.batch.packet"
	msgBatchSize          components.MessageKey = "outreach.batch.size"
	msgBatchHomogeneity   components.MessageKey = "outreach.batch.homogeneity"
	msgBatchSegment       components.MessageKey = "outreach.batch.segment"
	msgBatchSample        components.MessageKey = "outreach.batch.sample"
	msgBatchChannel       components.MessageKey = "outreach.batch.channel"
	msgBatchDigest        components.MessageKey = "outreach.batch.digest"
	msgBatchDifferences   components.MessageKey = "outreach.batch.differences"
	msgBatchReason        components.MessageKey = "outreach.batch.reason"
	msgBatchApprove       components.MessageKey = "outreach.batch.approve"
	msgBatchEmpty         components.MessageKey = "outreach.batch.empty"
	msgBatchExcluded      components.MessageKey = "outreach.batch.excluded"
	msgBatchExcludedEmpty components.MessageKey = "outreach.batch.excluded_empty"
	msgBatchIndividual    components.MessageKey = "outreach.batch.individual"
	msgBatchIndividualURL components.MessageKey = "outreach.batch.individual_url"
	msgBatchMore          components.MessageKey = "outreach.batch.more"

	msgStalledTitle            components.MessageKey = "outreach.stalled.title"
	msgStalledHint             components.MessageKey = "outreach.stalled.hint"
	msgStalledUnavail          components.MessageKey = "outreach.stalled.unavailable"
	msgStalledNotify           components.MessageKey = "outreach.stalled.notify"
	msgStalledNotifyTail       components.MessageKey = "outreach.stalled.notify_tail"
	msgStalledEmpty            components.MessageKey = "outreach.stalled.empty"
	msgEditorialTitle          components.MessageKey = "outreach.editorial.title"
	msgEditorialWarning        components.MessageKey = "outreach.editorial.warning"
	msgEditorialCampaign       components.MessageKey = "outreach.editorial.campaign"
	msgEditorialLead           components.MessageKey = "outreach.editorial.lead"
	msgEditorialMaterial       components.MessageKey = "outreach.editorial.material"
	msgEditorialVersion        components.MessageKey = "outreach.editorial.version"
	msgEditorialSubject        components.MessageKey = "outreach.editorial.subject"
	msgEditorialBody           components.MessageKey = "outreach.editorial.body"
	msgEditorialReason         components.MessageKey = "outreach.editorial.reason"
	msgEditorialApprove        components.MessageKey = "outreach.editorial.approve"
	msgEditorialReject         components.MessageKey = "outreach.editorial.reject"
	msgEditorialRecorded       components.MessageKey = "outreach.editorial.recorded"
	msgEditorialReasonRecorded components.MessageKey = "outreach.editorial.reason_recorded"
)

// consoleMessages возвращает каталог сообщений консоли рассылок: базовый каталог
// общего слоя, дополненный текстами экранов.
func consoleMessages() *components.Messages {
	return components.WithMessages(components.DefaultMessages(), outreachCatalog)
}
