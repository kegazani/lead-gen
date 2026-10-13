package httpapi

import "lead-gen/frontend/components"

// Каталог сообщений экранов заявок (web-frontend-architecture, срез 7, 7.1;
// design.md, Решение 12). Пользовательский текст шаблонов живёт здесь, а не в
// разметке: шаблон ссылается на ключ через функцию text. Базовые ключи (навигация)
// приходят из общего слоя и не дублируются.
var requestCatalog = map[components.MessageKey]string{
	"request.details.issue.search":      "В завершённом раунде сервис поиска был недоступен.",
	"request.details.issue.contacts":    "Часть страниц отклонена: не найдены пригодные контакты компании или источник является статьёй.",
	"request.details.issue.crawl":       "Часть сайтов не удалось обойти в завершённом раунде.",
	"request.details.issue.extract":     "Расход расширенного извлечения не подтверждён; часть сайтов пропущена.",
	"request.details.issue.deadline":    "Завершённый раунд достиг предела времени поиска.",
	"request.details.issue.budget":      "В завершённом раунде достигнут лимит бюджета поиска.",
	"request.details.issue.cancelled":   "Обход в завершённом раунде был остановлен.",
	"request.details.title":             "Подробности исполнения",
	"request.details.elapsed":           "Прошло с запуска:",
	"request.details.round":             "Раунд поиска:",
	"request.details.round_max":         "максимум",
	"request.details.sources":           "Откуда найдены лиды",
	"request.details.pool":              "Из существующей базы:",
	"request.details.crawl":             "Добавлено новым обходом:",
	"request.details.unknown":           "Нет данных",
	"request.details.sources_unknown":   "Происхождение лидов этого исполнения не записано. Нельзя определить, сколько взято из базы и сколько найдено обходом.",
	"request.details.saved":             "Писем сохранено:",
	"request.details.step_limit":        "Предел одной попытки шага:",
	"request.details.step_limit_hint":   "После ошибки шаг может повториться; это не предел общего времени заявки.",
	"request.details.step.search":       "Поиск и обход сайтов",
	"request.details.step.draft":        "Подготовка и сохранение писем",
	"request.details.step.transition":   "Обновление состояния заявки",
	"request.details.state.started":     "Выполняется",
	"request.details.state.scheduled":   "Ожидает исполнителя",
	"request.details.state.retry":       "Ожидает повторной попытки",
	"request.details.state.cancelled":   "Запрошена отмена",
	"request.details.state.paused":      "Приостановлено",
	"request.details.attempt":           "Попытка:",
	"request.details.running_for":       "Текущая попытка выполняется:",
	"request.details.heartbeat":         "Исполнитель подтверждал работу:",
	"request.details.ago":               "назад",
	"request.details.heartbeat_hint":    "Подтверждение связи с исполнителем не означает, что новый сайт уже обработан.",
	"request.details.retry_in":          "Следующая попытка через:",
	"request.details.failure":           "Предыдущая ошибка:",
	"request.details.failure.timeout":   "Превышено время ожидания",
	"request.details.failure.cancelled": "Шаг отменён",
	"request.details.failure.error":     "Ошибка выполнения шага",
	"request.details.duration":          "%d мин %d с",
	"request.details.searching":         "Ищем сайты и обходим их, чтобы добрать лидов до цели. При доборе уже найденные лиды сохраняются; счётчик меняется после завершения раунда.",
	"request.details.drafting":          "Поиск завершён. Письма создаются и сохраняются по одному; новые письма появляются ниже по мере сохранения.",
	"request.details.starting":          "Одобрение принято. Ожидаем начала исполнения заявки.",
	msgReqLoginTitle:                    "Вход",
	msgReqLoginUser:                     "Имя пользователя",
	msgReqLoginPass:                     "Пароль",
	msgReqLoginSubmit:                   "Войти",
	msgReqLoginError:                    "Неверный логин или пароль.",

	msgReqValidationQueueTitle: "Заявки",
	msgReqValidationQueueHint:  "Заявки клиентов и текущая стадия поиска и генерации писем. Список обновляется автоматически.",
	msgReqValidationQueueEmpty: "Заявок нет",
	msgReqProgressPending:      "Состояние исполнения ещё не получено",
	msgReqProgressRefreshError: "Обновление недоступно. Показано последнее полученное состояние.",
	msgReqProgressUpdated:      "Состояние заявки обновлено.",
	msgReqSearchRunning:        "Поиск запущен автоматически после одобрения. Сейчас система ищет сайты и обходит их. Число найденных обновляется после завершения раунда поиска; 0 на этом этапе ещё не означает отсутствие результатов. Эта карточка обновляется каждые 5 секунд.",
	msgReqExecutionLink:        "Открыть поиск и результат",
	msgReqExecutionStarted:     "Заявка одобрена. Поиск запускается автоматически; его состояние, найденные лиды и письма доступны в исполнении.",

	msgReqValidationTitlePrefix: "Валидация заявки",
	msgReqStatusLabel:           "Статус:",
	msgReqSaveOffer:             "Сохранить правку",
	msgReqTake:                  "Взять на проверку",
	msgReqCall:                  "Инициировать звонок-уточнение",
	msgReqApprove:               "Одобрить и начать поиск",
	msgReqReject:                "Отклонить",
	msgReqDecisionTitle:         "Решение валидации",
	msgReqDecisionOutcome:       "Исход:",
	msgReqDecisionActor:         "Актор:",
	msgReqDecisionAt:            "Время:",
	msgReqDecisionReason:        "Причина:",
	msgReqCallRef:               "Звонок:",
	msgReqOfferTitle:            "Оффер",
	msgReqOfferTarget:           "Цель:",
	msgReqOfferRevision:         "Версия оффера:",
	msgReqLeadTargetHint:        "Необязательно; пусто или 0 — без цели",

	msgReqApprovalQueueTitle:          "Очередь утверждения",
	msgReqApprovalQueueHint:           "Поиск по этим заявкам завершён и ждёт вашего решения: утвердить результат или отклонить.",
	msgReqRuntimeUnavailable:          "Исполнение недоступно: исполнитель заявок не подключён",
	msgReqFoundLeads:                  "Найдено лидов:",
	msgReqApprovalQueueEmpty:          "Заявок, ожидающих утверждения, нет",
	msgReqExecutionHistoryTitle:       "Исполнения заявок",
	msgReqExecutionHistoryEmpty:       "Запущенных исполнений пока нет",
	msgReqExecutionHistoryUnavailable: "Состояние исполнений временно недоступно",
	msgReqExecutionHistoryDrafted:     "Писем сформировано:",

	msgReqApprovalTitlePrefix:    "Утверждение заявки",
	msgReqApproveAction:          "Утвердить",
	msgReqExecutionTitle:         "Исполнение",
	msgReqEmailDraftsTitle:       "Сформированные письма",
	msgReqEmailDraftsEmpty:       "Для этой заявки писем пока нет.",
	msgReqEmailDraftsUnavailable: "Черновики писем сейчас недоступны.",
	msgReqEmailDraftLead:         "Найденный лид:",
	msgReqExecutionState:         "Состояние исполнения:",
	msgReqExecutionPhase:         "Этап:",
	msgReqShortfallTarget:        "Цель по лидам:",
	msgReqShortfallLabel:         "Недобор:",
	msgReqShortfallReached:       "Цель достигнута",
	msgReqShortfallNoLeads:       "Поиск не нашёл ни одного лида под критерий заявки.",
	msgReqShortfallPartial:       "Поиск остановился, не достигнув цели заявки: найдено меньше целевого числа компаний.",
	msgReqLiveOffer:              "Состояние заявки обновлено",
	msgReqLiveWorkflow:           "Состояние исполнения обновлено",

	// Общие подписи экранов заявок (change console-liquid-glass).
	msgReqTenant:       "Клиент:",
	msgReqNumber:       "№",
	msgReqVersion:      "версия",
	msgReqQueueOpen:    "Открыть",
	msgReqColRequest:   "Заявка",
	msgReqColPhase:     "Этап",
	msgReqColStatus:    "Статус",
	msgReqColFound:     "Найдено / цель",
	msgReqColShortfall: "Недобор",
	msgReqColAction:    "Действие",
	msgReqDecisionNote: "Решение необратимо: после утверждения заявка уходит в исполнение, после отклонения — отменяется.",
	msgReqEditTitle:    "Правка оффера",
	msgReqDecideTitle:  "Решение по заявке",
	msgReqTakeHint:     "Возьмите заявку на проверку, проверьте оффер и одобрите её, чтобы начать поиск.",
	msgReqCallHint:     "Если оффер неясен, уточните его у клиента звонком.",

	// Экран заявок компании: список с историей переходов и действиями возврата
	// на доработку и отмены.
	msgReqCompanyTitle:      "Заявки компании",
	msgReqCompanyHint:       "Все заявки выбранной компании с историей переходов.",
	msgReqCompanyEmpty:      "У компании нет заявок",
	msgReqHistoryTitle:      "История переходов",
	msgReqHistoryTransition: "Переход:",
	msgReqHistoryActor:      "Актор:",
	msgReqHistoryAt:         "Время:",
	msgReqHistoryReason:     "Причина:",
	msgReqReturnAction:      "Вернуть на доработку",
	msgReqCancelAction:      "Отменить",
	msgReqRerunAction:       "Повторить запуск",
	msgReqReturnHint:        "Объясните клиенту, что нужно исправить.",
	msgReqCancelHint:        "Отмена необратима; укажите причину.",
	msgReqRerunHint:         "Повторно запустить исполнение упавшей заявки.",

	// Читаемые подписи машинных кодов: статусы заявки, этапы и состояние
	// исполнения, исходы валидации. Шаблон берёт их функцией label; неизвестный
	// код показывается как есть, а не прячется.
	"request.status.submitted":  "Создана",
	"request.status.validating": "Создана",
	"request.status.validated":  "Создана",
	"request.status.processing": "В работе",
	"request.status.fulfilled":  "Выполнена",
	"request.status.rejected":   "Отклонена",
	"request.status.cancelled":  "Отменена",
	"request.status.failed":     "Сбой",

	"request.status.needs-revision": "Нужна доработка",

	"request.phase.starting-processing": "Запуск",
	"request.phase.searching":           "Поиск",
	"request.phase.drafting":            "Подготовка писем",
	"request.phase.handoff":             "Передача в рассылку",
	"request.phase.no-leads":            "Лидов не найдено",
	"request.phase.awaiting-approval":   "Ждёт решения",
	"request.phase.fulfilling":          "Исполнение",
	"request.phase.fulfilled":           "Завершено",
	"request.phase.cancelled":           "Отменена",
	"request.phase.failed":              "Сбой",
	"request.phase.drafts-generated":    "Письма сформированы",

	"request.execution.running":          "Выполняется",
	"request.execution.completed":        "Завершено",
	"request.execution.failed":           "Сбой",
	"request.execution.canceled":         "Отменено",
	"request.execution.cancelled":        "Отменено",
	"request.execution.terminated":       "Остановлено",
	"request.execution.timed-out":        "Истёк срок",
	"request.execution.continued-as-new": "Перезапущено",

	"request.decision.approved": "Одобрена",
	"request.decision.rejected": "Отклонена",
}

// Ключи каталога экранов заявок.
const (
	msgReqLoginTitle  components.MessageKey = "request.login.title"
	msgReqLoginUser   components.MessageKey = "request.login.username"
	msgReqLoginPass   components.MessageKey = "request.login.password"
	msgReqLoginSubmit components.MessageKey = "request.login.submit"
	msgReqLoginError  components.MessageKey = "request.login.error"

	msgReqValidationQueueTitle components.MessageKey = "request.validation_queue.title"
	msgReqValidationQueueHint  components.MessageKey = "request.validation_queue.hint"
	msgReqValidationQueueEmpty components.MessageKey = "request.validation_queue.empty"
	msgReqProgressPending      components.MessageKey = "request.progress.pending"
	msgReqProgressRefreshError components.MessageKey = "request.progress.refresh_error"
	msgReqProgressUpdated      components.MessageKey = "request.progress.updated"
	msgReqSearchRunning        components.MessageKey = "request.progress.search_running"
	msgReqExecutionLink        components.MessageKey = "request.validation.execution_link"
	msgReqExecutionStarted     components.MessageKey = "request.validation.execution_started"

	msgReqValidationTitlePrefix components.MessageKey = "request.validation.title"
	msgReqStatusLabel           components.MessageKey = "request.validation.status"
	msgReqSaveOffer             components.MessageKey = "request.validation.save_offer"
	msgReqTake                  components.MessageKey = "request.validation.take"
	msgReqCall                  components.MessageKey = "request.validation.call"
	msgReqApprove               components.MessageKey = "request.validation.approve"
	msgReqReject                components.MessageKey = "request.validation.reject"
	msgReqDecisionTitle         components.MessageKey = "request.validation.decision.title"
	msgReqDecisionOutcome       components.MessageKey = "request.validation.decision.outcome"
	msgReqDecisionActor         components.MessageKey = "request.validation.decision.actor"
	msgReqDecisionAt            components.MessageKey = "request.validation.decision.at"
	msgReqDecisionReason        components.MessageKey = "request.validation.decision.reason"
	msgReqCallRef               components.MessageKey = "request.validation.call_ref"
	msgReqOfferTitle            components.MessageKey = "request.offer.title"
	msgReqOfferTarget           components.MessageKey = "request.offer.target"
	msgReqOfferRevision         components.MessageKey = "request.offer.revision"
	msgReqLeadTargetHint        components.MessageKey = "request.validation.lead_target_hint"

	msgReqApprovalQueueTitle          components.MessageKey = "request.approval_queue.title"
	msgReqApprovalQueueHint           components.MessageKey = "request.approval_queue.hint"
	msgReqRuntimeUnavailable          components.MessageKey = "request.approval_queue.unavailable"
	msgReqFoundLeads                  components.MessageKey = "request.approval_queue.found"
	msgReqApprovalQueueEmpty          components.MessageKey = "request.approval_queue.empty"
	msgReqExecutionHistoryTitle       components.MessageKey = "request.execution_history.title"
	msgReqExecutionHistoryEmpty       components.MessageKey = "request.execution_history.empty"
	msgReqExecutionHistoryUnavailable components.MessageKey = "request.execution_history.unavailable"
	msgReqExecutionHistoryDrafted     components.MessageKey = "request.execution_history.drafted"

	msgReqApprovalTitlePrefix    components.MessageKey = "request.approval.title"
	msgReqApproveAction          components.MessageKey = "request.approval.approve"
	msgReqExecutionTitle         components.MessageKey = "request.approval.execution.title"
	msgReqEmailDraftsTitle       components.MessageKey = "request.approval.email_drafts.title"
	msgReqEmailDraftsEmpty       components.MessageKey = "request.approval.email_drafts.empty"
	msgReqEmailDraftsUnavailable components.MessageKey = "request.approval.email_drafts.unavailable"
	msgReqEmailDraftLead         components.MessageKey = "request.approval.email_drafts.lead"
	msgReqExecutionState         components.MessageKey = "request.approval.execution.state"
	msgReqExecutionPhase         components.MessageKey = "request.approval.execution.phase"
	msgReqShortfallTarget        components.MessageKey = "request.approval.target"
	msgReqShortfallLabel         components.MessageKey = "request.approval.shortfall"
	msgReqShortfallReached       components.MessageKey = "request.approval.shortfall.reached"
	msgReqShortfallNoLeads       components.MessageKey = "request.approval.shortfall.no_leads"
	msgReqShortfallPartial       components.MessageKey = "request.approval.shortfall.partial"
	msgReqLiveOffer              components.MessageKey = "request.live.offer"
	msgReqLiveWorkflow           components.MessageKey = "request.live.workflow"

	msgReqTenant       components.MessageKey = "request.tenant"
	msgReqNumber       components.MessageKey = "request.number"
	msgReqVersion      components.MessageKey = "request.version"
	msgReqQueueOpen    components.MessageKey = "request.queue.open"
	msgReqColRequest   components.MessageKey = "request.queue.col.request"
	msgReqColPhase     components.MessageKey = "request.queue.col.phase"
	msgReqColStatus    components.MessageKey = "request.queue.col.status"
	msgReqColFound     components.MessageKey = "request.queue.col.found"
	msgReqColShortfall components.MessageKey = "request.queue.col.shortfall"
	msgReqColAction    components.MessageKey = "request.queue.col.action"
	msgReqDecisionNote components.MessageKey = "request.approval.decision.note"
	msgReqEditTitle    components.MessageKey = "request.validation.edit.title"
	msgReqDecideTitle  components.MessageKey = "request.validation.decide.title"
	msgReqTakeHint     components.MessageKey = "request.validation.take.hint"
	msgReqCallHint     components.MessageKey = "request.validation.call.hint"

	msgReqCompanyTitle      components.MessageKey = "request.company.title"
	msgReqCompanyHint       components.MessageKey = "request.company.hint"
	msgReqCompanyEmpty      components.MessageKey = "request.company.empty"
	msgReqHistoryTitle      components.MessageKey = "request.company.history.title"
	msgReqHistoryTransition components.MessageKey = "request.company.history.transition"
	msgReqHistoryActor      components.MessageKey = "request.company.history.actor"
	msgReqHistoryAt         components.MessageKey = "request.company.history.at"
	msgReqHistoryReason     components.MessageKey = "request.company.history.reason"
	msgReqReturnAction      components.MessageKey = "request.company.return"
	msgReqCancelAction      components.MessageKey = "request.company.cancel"
	msgReqRerunAction       components.MessageKey = "request.company.rerun"
	msgReqReturnHint        components.MessageKey = "request.company.return.hint"
	msgReqCancelHint        components.MessageKey = "request.company.cancel.hint"
	msgReqRerunHint         components.MessageKey = "request.company.rerun.hint"
)

// consoleMessages возвращает каталог сообщений экранов заявок: базовый каталог
// общего слоя, дополненный текстами экранов.
func consoleMessages() *components.Messages {
	return components.WithMessages(components.DefaultMessages(), requestCatalog)
}

// consoleLive — область живых объявлений консоли заявок: общий скрипт
// console-a11y.js объявляет в ней изменения после подмены фрагмента (7.3).
var consoleLive = components.Live{ID: "console-live"}
