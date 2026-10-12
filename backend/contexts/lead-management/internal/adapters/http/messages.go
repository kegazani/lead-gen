package httpapi

import "lead-gen/frontend/components"

// Каталог сообщений консоли лидов (web-frontend-architecture, срез 7, 7.1;
// design.md, Решение 12). Пользовательский текст шаблонов живёт здесь, а не в
// разметке: шаблон ссылается на ключ через функцию text. Базовые ключи (навигация,
// статусы верификации) приходят из общего слоя и не дублируются.
var leadCatalog = map[components.MessageKey]string{
	// Вход.
	msgLeadLoginTitle:  "Вход",
	msgLeadLoginUser:   "Имя пользователя",
	msgLeadLoginPass:   "Пароль",
	msgLeadLoginSubmit: "Войти",

	// Главная и журнал заданий.
	msgLeadHomeTitle:         "Главная",
	msgLeadJournalTitle:      "Журнал заданий",
	msgLeadJournalSelectAll:  "Выбрать все задания",
	msgLeadJournalAll:        "Все",
	msgLeadJournalCancel:     "Отменить выбранные",
	msgLeadJournalSelected:   "выбрано: 0",
	msgLeadJournalSelectOne:  "Выбрать задание",
	msgLeadJournalQuery:      "Запрос",
	msgLeadJournalProgress:   "Ход",
	msgLeadJournalOpen:       "Открыть полностью",
	msgLeadJournalEmpty:      "Заданий пока нет",
	msgLeadFirecrawlTitle:    "Firecrawl",
	msgLeadFirecrawlCredits:  "Остаток кредитов Firecrawl:",
	msgLeadFirecrawlUnavail:  "Недоступен",
	msgLeadFirecrawlUpdated:  "(обновлено",
	msgLeadFirecrawlTokens:   "Токены Firecrawl:",
	msgLeadNewSearchTitle:    "Новый поиск",
	msgLeadNewSearchSubmit:   "Запустить поиск",
	msgLeadNewSearchHint:     "Поиск находит сайты по запросу, обходит их и собирает контакты в карточки. «Сколько найти» останавливает задание по достижении N уникальных компаний (пусто — прежнее поведение); «Результатов на запрос» ограничивает выдачу поиска; «Свои цели» задают цели обхода вручную, по строке. Бюджет кредитов Firecrawl ограничивает стоимость запроса.",
	msgLeadRecentTitle:       "Последние лиды",
	msgLeadHeaderCompany:     "Компания",
	msgLeadHeaderDescription: "Описание",
	msgLeadRecentEmpty:       "Лидов пока нет",

	// Задание.
	msgLeadJobTitle:          "Задание",
	msgLeadJobQuery:          "Запрос",
	msgLeadJobSummary:        "Сводка",
	msgLeadJobCreated:        "Создано",
	msgLeadJobUpdated:        "Обновлено",
	msgLeadJobFoundCompanies: "Найдено компаний",
	msgLeadJobBudget:         "Бюджет",
	msgLeadJobProgress:       "Ход",
	msgLeadJobDistribution:   "Распределение оценок",
	msgLeadVerdictFits:       "Подходит",
	msgLeadVerdictHypothesis: "Гипотеза",
	msgLeadVerdictUnsuitable: "Не подходит",
	msgLeadVerdictUnassessed: "Не оценено",
	msgLeadJobNoScores:       "Оценок пока нет",
	msgLeadJobOpenResults:    "Открыть список результатов по оценкам",
	msgLeadJobEvidenceTitle:  "Подходящие: основания и источники",
	msgLeadJobNoFits:         "Подходящих компаний нет",
	msgLeadJobCompaniesTitle: "Найденные компании",
	msgLeadNoEmail:           "нет email",
	msgLeadEmailUnavailable:  "email недоступен",
	msgLeadJobNoCompanies:    "Компании не найдены",
	msgLeadJobTimelineTitle:  "Ход выполнения",
	msgLeadJobTimelineEmpty:  "Ход выполнения не записан",
	msgLeadJobBack:           "← К журналу",
	msgLeadJobReassess:       "Переоценить компании",
	msgLeadJobNoEmailAria:    " — нет email",
	msgLeadJobEmailAria:      " — email недоступен",

	// Сводка отбора, диагноз и причины отсева задания.
	msgLeadJobSelectionTitle:         "Сводка отбора",
	msgLeadJobTarget:                 "Цель по лидам",
	msgLeadJobFound:                  "Найдено",
	msgLeadJobShortfall:              "Недобор",
	msgLeadJobTargetReached:          "Цель достигнута",
	msgLeadJobCoverageTitle:          "Охват поиска",
	msgLeadJobCoveragePages:          "Обойдено страниц",
	msgLeadJobCoverageCompanies:      "Найдено компаний",
	msgLeadJobCoveragePlannedTargets: "Целей плана",
	msgLeadJobCoveragePlannedURLs:    "URL в плане",
	msgLeadJobCoverageParseErrors:    "Ошибок разбора",
	msgLeadJobCoverageAssessErrors:   "Ошибок оценки",
	msgLeadJobCoverageCredits:        "Кредиты",
	msgLeadJobCoverageTokens:         "Токены",
	msgLeadJobCoverageStop:           "Причина остановки",
	msgLeadJobCoverageUnavailable:    "Недоступно",
	msgLeadJobFunnelTitle:            "Воронка отбора",
	msgLeadJobFunnelUnavailable:      "недоступно",
	msgLeadJobGroupsTitle:            "Причины отсева",
	msgLeadJobGroupsEmpty:            "Отсева нет",
	msgLeadJobGroupsToList:           "К списку",
	msgLeadJobDiagnosisTitle:         "Диагноз",
	msgLeadJobDiagnosisRemainder:     "Наблюдаемый остаток",
	msgLeadJobDiagnosisNoReason:      "Однозначная причина не определена — приведены наблюдаемые счётчики.",

	// Свидетельства.
	msgLeadEvidenceCriterion: "Критерий",
	msgLeadEvidenceFact:      "Факт",
	msgLeadEvidenceSource:    "Источник",
	msgLeadEvidenceReasoning: "Объяснение связи",
	msgLeadEvidenceSignal:    "Тип сигнала",
	msgLeadEvidenceMissing:   "Не хватает",
	msgLeadEvidenceMismatch:  "Несоответствие",
	msgLeadEvidenceEmpty:     "Свидетельств нет",

	// Список карточек.
	msgLeadCardsTitle:           "Карточки",
	msgLeadCardsJobScoped:       "Состав по оценкам задания",
	msgLeadCardsCompositionAria: "Состав результатов",
	msgLeadCardsFits:            "Подходящие",
	msgLeadCardsOther:           "Неподходящие и гипотезы",
	msgLeadCardsViewAria:        "Вид списка карточек",
	msgLeadCardsTiles:           "Плитки",
	msgLeadCardsRows:            "Строки",
	msgLeadCardsEmpty:           "Карточек пока нет",
	msgLeadCardsFitsEmpty:       "Подходящих компаний пока нет",
	msgLeadCardsOtherEmpty:      "Неподходящих компаний и гипотез пока нет",
	msgLeadCardsHypothesisEmpty: "Гипотез пока нет",
	msgLeadCardsUnsuitableEmpty: "Неподходящих компаний пока нет",
	msgLeadCardsUnassessedEmpty: "Неоценённых компаний пока нет",
	msgLeadCardsFilterTitle:     "Фильтр по причине",
	msgLeadCardsFilterClear:     "Снять фильтр",

	// Карточка лида.
	msgLeadCardTitle:            "Карточка",
	msgLeadCardSnoozed:          "Отложен",
	msgLeadCardStage:            "Стадия",
	msgLeadCardAssessmentTitle:  "Почему эта оценка?",
	msgLeadCardSignalPrefix:     "Тип сигнала:",
	msgLeadCardNoAssessment:     "В этом задании оценка компании не определена.",
	msgLeadCardAssessmentScoped: "Оценка привязана к заданию: откройте карточку из результатов задания, чтобы увидеть вердикт и свидетельства.",
	msgLeadCardSocials:          "Соцсети",
	msgLeadCardContacts:         "Контакты",
	msgLeadHeaderEmail:          "Email",
	msgLeadHeaderPhone:          "Телефон",
	msgLeadHeaderRole:           "Роль",
	msgLeadHeaderSource:         "Источник",
	msgLeadHeaderVerification:   "Верификация",
	msgLeadCardContactsEmpty:    "Контактов пока нет",
	msgLeadCardHistory:          "Переписка",
	msgLeadCardHistoryEmpty:     "Переписки пока нет",
	msgLeadCardSnooze:           "Отложить",
	msgLeadCardDays:             "Дней",
	msgLeadCardSnoozeSubmit:     "Отложить",
	msgLeadCardSend:             "Отправить письмо",
	msgLeadCardTemplate:         "Шаблон",
	msgLeadCardTemplateChoose:   "— выберите шаблон —",
	msgLeadCardPreview:          "Предпросмотр",
	msgLeadCardSendSubmit:       "Отправить",
	msgLeadCardTemplateLibrary:  "Библиотека шаблонов",
	msgLeadCardHide:             "Скрыть",
	msgLeadCardDelete:           "Удалить",

	// Воронка.
	msgLeadFunnelTitle:    "Воронка",
	msgLeadFunnelHint:     "Перетащите лида в колонку этапа. Перенос доступен и без перетаскивания: выберите этап в форме карточки и подтвердите — тот же путь работает с клавиатуры и при отключённом JavaScript.",
	msgLeadStageNotWarmed: "Не прогрет",
	msgLeadStageEmailSent: "Письмо отправлено",
	msgLeadStageWarming:   "Прогрев",
	msgLeadStageCold:      "Холодный",
	msgLeadStageMove:      "Этап для переноса",
	msgLeadStageSubmit:    "Перенести",
	msgLeadFunnelEmpty:    "Лидов нет",

	// Лиды с email.
	msgLeadLeadsTitle:       "Лиды с email",
	msgLeadLeadsHint:        "Все лиды, у которых найден адрес электронной почты: ссылка на сайт, название и адрес.",
	msgLeadLeadsDownload:    "Скачать HTML-файл",
	msgLeadLeadsDownloadCSV: "Скачать CSV",
	msgLeadLeadsHeaderSite:  "Сайт",
	msgLeadLeadsHeaderName:  "Название",
	msgLeadLeadsEmpty:       "Лидов с email пока нет",

	// Отправка.
	msgLeadSendTitle:   "Отправка",
	msgLeadSendResult:  "Итог рассылки",
	msgLeadSendSent:    "Отправлено:",
	msgLeadSendSkipped: "пропущено:",
	msgLeadSendSegment: "Рассылка по сегменту",
	msgLeadSendSubmit:  "Разослать",
	msgLeadSendLibrary: "Библиотека шаблонов",

	// Библиотека шаблонов.
	msgLeadTplNotFound:    "Шаблонов не найдено. Измените запрос или создайте новый шаблон.",
	msgLeadTplSearch:      "Поиск шаблона",
	msgLeadTplFind:        "Найти",
	msgLeadTplValues:      "Значения переменных",
	msgLeadTplVariables:   "Переменные шаблона",
	msgLeadTplInsert:      "Вставить в текст",
	msgLeadTplPreview:     "Предпросмотр",
	msgLeadTplRef:         "Ссылка на шаблон:",
	msgLeadTplSubject:     "Тема",
	msgLeadTplBody:        "Текст",
	msgLeadTplPreviewHint: "Выберите шаблон, чтобы увидеть предпросмотр собранного письма.",
	msgLeadTplLibrary:     "Библиотека шаблонов",
	msgLeadTplEdit:        "Правка шаблона",
	msgLeadTplNew:         "Новый шаблон",
	msgLeadTplRevision:    "Редакция:",
	msgLeadTplName:        "Имя",
	msgLeadTplBodyLabel:   "Текст письма",
	msgLeadTplSave:        "Сохранить",
	msgLeadTplCreate:      "Создать",
	msgLeadTplDelete:      "Удалить",
	msgLeadTplList:        "Список шаблонов",
	msgLeadTplFallback:    "fallback",

	// Ассистент.
	msgLeadAssistantTitle:         "Ассистент",
	msgLeadAssistantHint:          "Опишите, каких клиентов ищете. Ассистент задаст уточняющие вопросы и соберёт план. Операторские параметры — глубина и время работы в секундах — согласуются в разговоре, а цели, целевое число компаний и лимит поиска можно править прямо в панели плана перед запуском.",
	msgLeadAssistantDialogue:      "Диалог",
	msgLeadAssistantDialogueEmpty: "Опишите, каких клиентов ищете, — ассистент задаст уточняющие вопросы.",
	msgLeadAssistantAnswer:        "Ваш ответ",
	msgLeadAssistantSend:          "Отправить",
	msgLeadAssistantLaunch:        "Запустить краулинг",
	msgLeadAssistantProbe:         "Проверочная сессия",
	msgLeadAssistantInterpret:     "Как система поняла запрос",
	msgLeadAssistantUndetermined:  "Не удалось определить критерии:",
	msgLeadAssistantUncertain:     "Недостаточно данных, чтобы определить признаки потребности. Уточните запрос — критерии не выдумываются.",
	msgLeadAssistantTargetLead:    "Целевой лид",
	msgLeadAssistantNeedSignals:   "Признаки потребности",
	msgLeadAssistantExclusions:    "Исключения",
	msgLeadAssistantHypothesis:    "Признаки гипотезы",
	msgLeadAssistantNoInterpret:   "Интерпретация не определена.",
	msgLeadAssistantPlan:          "Технический план обхода",
	msgLeadAssistantCompanies:     "Сколько найти компаний",
	msgLeadAssistantResults:       "Результатов на запрос",
	msgLeadAssistantTargets:       "Свои цели (по строке — запрос или URL)",
	msgLeadAssistantReady:         "Ассистент считает план готовым — можно запускать.",

	// Форумы.
	msgLeadForumTitle:   "Форумы",
	msgLeadForumSearch:  "Поиск сигналов",
	msgLeadForumFind:    "Искать",
	msgLeadForumByQuery: "Сигналы по запросу",
	msgLeadForumEmpty:   "Сигналов не найдено",

	// Автономная выгрузка лидов.
	msgLeadExportTitle:         "Лиды с email",
	msgLeadExportCount:         "из",
	msgLeadExportPlaceholder:   "Поиск по email, компании или сайту…",
	msgLeadExportSearchAria:    "Поиск по лидам",
	msgLeadExportClear:         "Очистить",
	msgLeadExportHeaderEmail:   "Email",
	msgLeadExportHeaderCompany: "Компания",
	msgLeadExportHeaderLink:    "Ссылка",
	msgLeadExportOpenSite:      "Открыть сайт",
	msgLeadExportWrite:         "Написать",
	msgLeadExportEmpty:         "Лидов с email пока нет",
	msgLeadExportFooter:        "Сформировано автоматически",

	// Живые объявления консоли.
	msgLeadLiveJournal: "Журнал заданий обновлён",
	msgLeadLiveCard:    "Карточка обновлена",
	msgLeadLiveSend:    "Список шаблонов обновлён",
}

// Ключи каталога консоли лидов.
const (
	msgLeadLoginTitle  components.MessageKey = "lead.login.title"
	msgLeadLoginUser   components.MessageKey = "lead.login.username"
	msgLeadLoginPass   components.MessageKey = "lead.login.password"
	msgLeadLoginSubmit components.MessageKey = "lead.login.submit"

	msgLeadHomeTitle         components.MessageKey = "lead.home.title"
	msgLeadJournalTitle      components.MessageKey = "lead.journal.title"
	msgLeadJournalSelectAll  components.MessageKey = "lead.journal.select_all"
	msgLeadJournalAll        components.MessageKey = "lead.journal.all"
	msgLeadJournalCancel     components.MessageKey = "lead.journal.cancel"
	msgLeadJournalSelected   components.MessageKey = "lead.journal.selected"
	msgLeadJournalSelectOne  components.MessageKey = "lead.journal.select_one"
	msgLeadJournalQuery      components.MessageKey = "lead.journal.query"
	msgLeadJournalProgress   components.MessageKey = "lead.journal.progress"
	msgLeadJournalOpen       components.MessageKey = "lead.journal.open"
	msgLeadJournalEmpty      components.MessageKey = "lead.journal.empty"
	msgLeadFirecrawlTitle    components.MessageKey = "lead.home.firecrawl.title"
	msgLeadFirecrawlCredits  components.MessageKey = "lead.home.firecrawl.credits"
	msgLeadFirecrawlUnavail  components.MessageKey = "lead.home.firecrawl.unavailable"
	msgLeadFirecrawlUpdated  components.MessageKey = "lead.home.firecrawl.updated"
	msgLeadFirecrawlTokens   components.MessageKey = "lead.home.firecrawl.tokens"
	msgLeadNewSearchTitle    components.MessageKey = "lead.home.new_search.title"
	msgLeadNewSearchSubmit   components.MessageKey = "lead.home.new_search.submit"
	msgLeadNewSearchHint     components.MessageKey = "lead.home.new_search.hint"
	msgLeadRecentTitle       components.MessageKey = "lead.home.recent.title"
	msgLeadHeaderCompany     components.MessageKey = "lead.header.company"
	msgLeadHeaderDescription components.MessageKey = "lead.header.description"
	msgLeadRecentEmpty       components.MessageKey = "lead.home.recent.empty"

	msgLeadJobTitle          components.MessageKey = "lead.job.title"
	msgLeadJobQuery          components.MessageKey = "lead.job.query"
	msgLeadJobSummary        components.MessageKey = "lead.job.summary"
	msgLeadJobCreated        components.MessageKey = "lead.job.created"
	msgLeadJobUpdated        components.MessageKey = "lead.job.updated"
	msgLeadJobFoundCompanies components.MessageKey = "lead.job.found_companies"
	msgLeadJobBudget         components.MessageKey = "lead.job.budget"
	msgLeadJobProgress       components.MessageKey = "lead.job.progress"
	msgLeadJobDistribution   components.MessageKey = "lead.job.distribution"
	msgLeadVerdictFits       components.MessageKey = "lead.verdict.fits"
	msgLeadVerdictHypothesis components.MessageKey = "lead.verdict.hypothesis"
	msgLeadVerdictUnsuitable components.MessageKey = "lead.verdict.unsuitable"
	msgLeadVerdictUnassessed components.MessageKey = "lead.verdict.unassessed"
	msgLeadJobNoScores       components.MessageKey = "lead.job.no_scores"
	msgLeadJobOpenResults    components.MessageKey = "lead.job.open_results"
	msgLeadJobEvidenceTitle  components.MessageKey = "lead.job.evidence_title"
	msgLeadJobNoFits         components.MessageKey = "lead.job.no_fits"
	msgLeadJobCompaniesTitle components.MessageKey = "lead.job.companies_title"
	msgLeadNoEmail           components.MessageKey = "lead.company.no_email"
	msgLeadEmailUnavailable  components.MessageKey = "lead.company.email_unavailable"
	msgLeadJobNoCompanies    components.MessageKey = "lead.job.no_companies"
	msgLeadJobTimelineTitle  components.MessageKey = "lead.job.timeline_title"
	msgLeadJobTimelineEmpty  components.MessageKey = "lead.job.timeline_empty"
	msgLeadJobBack           components.MessageKey = "lead.job.back"
	msgLeadJobReassess       components.MessageKey = "lead.job.reassess"
	msgLeadJobNoEmailAria    components.MessageKey = "lead.job.no_email_aria"
	msgLeadJobEmailAria      components.MessageKey = "lead.job.email_aria"

	msgLeadJobSelectionTitle         components.MessageKey = "lead.job.selection.title"
	msgLeadJobTarget                 components.MessageKey = "lead.job.selection.target"
	msgLeadJobFound                  components.MessageKey = "lead.job.selection.found"
	msgLeadJobShortfall              components.MessageKey = "lead.job.selection.shortfall"
	msgLeadJobTargetReached          components.MessageKey = "lead.job.selection.reached"
	msgLeadJobCoverageTitle          components.MessageKey = "lead.job.coverage.title"
	msgLeadJobCoveragePages          components.MessageKey = "lead.job.coverage.pages"
	msgLeadJobCoverageCompanies      components.MessageKey = "lead.job.coverage.companies"
	msgLeadJobCoveragePlannedTargets components.MessageKey = "lead.job.coverage.planned_targets"
	msgLeadJobCoveragePlannedURLs    components.MessageKey = "lead.job.coverage.planned_urls"
	msgLeadJobCoverageParseErrors    components.MessageKey = "lead.job.coverage.parse_errors"
	msgLeadJobCoverageAssessErrors   components.MessageKey = "lead.job.coverage.assessment_errors"
	msgLeadJobCoverageCredits        components.MessageKey = "lead.job.coverage.credits"
	msgLeadJobCoverageTokens         components.MessageKey = "lead.job.coverage.tokens"
	msgLeadJobCoverageStop           components.MessageKey = "lead.job.coverage.stop_reason"
	msgLeadJobCoverageUnavailable    components.MessageKey = "lead.job.coverage.unavailable"
	msgLeadJobFunnelTitle            components.MessageKey = "lead.job.funnel.title"
	msgLeadJobFunnelUnavailable      components.MessageKey = "lead.job.funnel.unavailable"
	msgLeadJobGroupsTitle            components.MessageKey = "lead.job.groups.title"
	msgLeadJobGroupsEmpty            components.MessageKey = "lead.job.groups.empty"
	msgLeadJobGroupsToList           components.MessageKey = "lead.job.groups.to_list"
	msgLeadJobDiagnosisTitle         components.MessageKey = "lead.job.diagnosis.title"
	msgLeadJobDiagnosisRemainder     components.MessageKey = "lead.job.diagnosis.remainder"
	msgLeadJobDiagnosisNoReason      components.MessageKey = "lead.job.diagnosis.no_reason"

	msgLeadEvidenceCriterion components.MessageKey = "lead.evidence.criterion"
	msgLeadEvidenceFact      components.MessageKey = "lead.evidence.fact"
	msgLeadEvidenceSource    components.MessageKey = "lead.evidence.source"
	msgLeadEvidenceReasoning components.MessageKey = "lead.evidence.reasoning"
	msgLeadEvidenceSignal    components.MessageKey = "lead.evidence.signal"
	msgLeadEvidenceMissing   components.MessageKey = "lead.evidence.missing"
	msgLeadEvidenceMismatch  components.MessageKey = "lead.evidence.mismatch"
	msgLeadEvidenceEmpty     components.MessageKey = "lead.evidence.empty"

	msgLeadCardsTitle           components.MessageKey = "lead.cards.title"
	msgLeadCardsJobScoped       components.MessageKey = "lead.cards.job_scoped"
	msgLeadCardsCompositionAria components.MessageKey = "lead.cards.composition_aria"
	msgLeadCardsFits            components.MessageKey = "lead.cards.fits"
	msgLeadCardsOther           components.MessageKey = "lead.cards.other"
	msgLeadCardsViewAria        components.MessageKey = "lead.cards.view_aria"
	msgLeadCardsTiles           components.MessageKey = "lead.cards.tiles"
	msgLeadCardsRows            components.MessageKey = "lead.cards.rows"
	msgLeadCardsEmpty           components.MessageKey = "lead.cards.empty"
	msgLeadCardsFitsEmpty       components.MessageKey = "lead.cards.fits_empty"
	msgLeadCardsOtherEmpty      components.MessageKey = "lead.cards.other_empty"
	msgLeadCardsHypothesisEmpty components.MessageKey = "lead.cards.hypothesis_empty"
	msgLeadCardsUnsuitableEmpty components.MessageKey = "lead.cards.unsuitable_empty"
	msgLeadCardsUnassessedEmpty components.MessageKey = "lead.cards.unassessed_empty"
	msgLeadCardsFilterTitle     components.MessageKey = "lead.cards.filter.title"
	msgLeadCardsFilterClear     components.MessageKey = "lead.cards.filter.clear"

	msgLeadCardTitle            components.MessageKey = "lead.card.title"
	msgLeadCardSnoozed          components.MessageKey = "lead.card.snoozed"
	msgLeadCardStage            components.MessageKey = "lead.card.stage"
	msgLeadCardAssessmentTitle  components.MessageKey = "lead.card.assessment_title"
	msgLeadCardSignalPrefix     components.MessageKey = "lead.card.signal_prefix"
	msgLeadCardNoAssessment     components.MessageKey = "lead.card.no_assessment"
	msgLeadCardAssessmentScoped components.MessageKey = "lead.card.assessment_scoped"
	msgLeadCardSocials          components.MessageKey = "lead.card.socials"
	msgLeadCardContacts         components.MessageKey = "lead.card.contacts"
	msgLeadHeaderEmail          components.MessageKey = "lead.header.email"
	msgLeadHeaderPhone          components.MessageKey = "lead.header.phone"
	msgLeadHeaderRole           components.MessageKey = "lead.header.role"
	msgLeadHeaderSource         components.MessageKey = "lead.header.source"
	msgLeadHeaderVerification   components.MessageKey = "lead.header.verification"
	msgLeadCardContactsEmpty    components.MessageKey = "lead.card.contacts_empty"
	msgLeadCardHistory          components.MessageKey = "lead.card.history"
	msgLeadCardHistoryEmpty     components.MessageKey = "lead.card.history_empty"
	msgLeadCardSnooze           components.MessageKey = "lead.card.snooze"
	msgLeadCardDays             components.MessageKey = "lead.card.days"
	msgLeadCardSnoozeSubmit     components.MessageKey = "lead.card.snooze_submit"
	msgLeadCardSend             components.MessageKey = "lead.card.send"
	msgLeadCardTemplate         components.MessageKey = "lead.card.template"
	msgLeadCardTemplateChoose   components.MessageKey = "lead.card.template_choose"
	msgLeadCardPreview          components.MessageKey = "lead.card.preview"
	msgLeadCardSendSubmit       components.MessageKey = "lead.card.send_submit"
	msgLeadCardTemplateLibrary  components.MessageKey = "lead.card.template_library"
	msgLeadCardHide             components.MessageKey = "lead.card.hide"
	msgLeadCardDelete           components.MessageKey = "lead.card.delete"

	msgLeadFunnelTitle    components.MessageKey = "lead.funnel.title"
	msgLeadFunnelHint     components.MessageKey = "lead.funnel.hint"
	msgLeadStageNotWarmed components.MessageKey = "lead.stage.not_warmed"
	msgLeadStageEmailSent components.MessageKey = "lead.stage.email_sent"
	msgLeadStageWarming   components.MessageKey = "lead.stage.warming"
	msgLeadStageCold      components.MessageKey = "lead.stage.cold"
	msgLeadStageMove      components.MessageKey = "lead.stage.move"
	msgLeadStageSubmit    components.MessageKey = "lead.stage.submit"
	msgLeadFunnelEmpty    components.MessageKey = "lead.funnel.empty"

	msgLeadLeadsTitle       components.MessageKey = "lead.leads.title"
	msgLeadLeadsHint        components.MessageKey = "lead.leads.hint"
	msgLeadLeadsDownload    components.MessageKey = "lead.leads.download_html"
	msgLeadLeadsDownloadCSV components.MessageKey = "lead.leads.download_csv"
	msgLeadLeadsHeaderSite  components.MessageKey = "lead.leads.header_site"
	msgLeadLeadsHeaderName  components.MessageKey = "lead.leads.header_name"
	msgLeadLeadsEmpty       components.MessageKey = "lead.leads.empty"

	msgLeadSendTitle   components.MessageKey = "lead.send.title"
	msgLeadSendResult  components.MessageKey = "lead.send.result"
	msgLeadSendSent    components.MessageKey = "lead.send.sent"
	msgLeadSendSkipped components.MessageKey = "lead.send.skipped"
	msgLeadSendSegment components.MessageKey = "lead.send.segment"
	msgLeadSendSubmit  components.MessageKey = "lead.send.submit"
	msgLeadSendLibrary components.MessageKey = "lead.send.library"

	msgLeadTplNotFound    components.MessageKey = "lead.template.not_found"
	msgLeadTplSearch      components.MessageKey = "lead.template.search"
	msgLeadTplFind        components.MessageKey = "lead.template.find"
	msgLeadTplValues      components.MessageKey = "lead.template.values"
	msgLeadTplVariables   components.MessageKey = "lead.template.variables"
	msgLeadTplInsert      components.MessageKey = "lead.template.insert"
	msgLeadTplPreview     components.MessageKey = "lead.template.preview"
	msgLeadTplRef         components.MessageKey = "lead.template.ref"
	msgLeadTplSubject     components.MessageKey = "lead.template.subject"
	msgLeadTplBody        components.MessageKey = "lead.template.body"
	msgLeadTplPreviewHint components.MessageKey = "lead.template.preview_hint"
	msgLeadTplLibrary     components.MessageKey = "lead.template.library"
	msgLeadTplEdit        components.MessageKey = "lead.template.edit"
	msgLeadTplNew         components.MessageKey = "lead.template.new"
	msgLeadTplRevision    components.MessageKey = "lead.template.revision"
	msgLeadTplName        components.MessageKey = "lead.template.name"
	msgLeadTplBodyLabel   components.MessageKey = "lead.template.body_label"
	msgLeadTplSave        components.MessageKey = "lead.template.save"
	msgLeadTplCreate      components.MessageKey = "lead.template.create"
	msgLeadTplDelete      components.MessageKey = "lead.template.delete"
	msgLeadTplList        components.MessageKey = "lead.template.list"
	msgLeadTplFallback    components.MessageKey = "lead.template.fallback"

	msgLeadAssistantTitle         components.MessageKey = "lead.assistant.title"
	msgLeadAssistantHint          components.MessageKey = "lead.assistant.hint"
	msgLeadAssistantDialogue      components.MessageKey = "lead.assistant.dialogue"
	msgLeadAssistantDialogueEmpty components.MessageKey = "lead.assistant.dialogue_empty"
	msgLeadAssistantAnswer        components.MessageKey = "lead.assistant.answer"
	msgLeadAssistantSend          components.MessageKey = "lead.assistant.send"
	msgLeadAssistantLaunch        components.MessageKey = "lead.assistant.launch"
	msgLeadAssistantProbe         components.MessageKey = "lead.assistant.probe"
	msgLeadAssistantInterpret     components.MessageKey = "lead.assistant.interpret"
	msgLeadAssistantUndetermined  components.MessageKey = "lead.assistant.undetermined"
	msgLeadAssistantUncertain     components.MessageKey = "lead.assistant.uncertain"
	msgLeadAssistantTargetLead    components.MessageKey = "lead.assistant.target_lead"
	msgLeadAssistantNeedSignals   components.MessageKey = "lead.assistant.need_signals"
	msgLeadAssistantExclusions    components.MessageKey = "lead.assistant.exclusions"
	msgLeadAssistantHypothesis    components.MessageKey = "lead.assistant.hypothesis"
	msgLeadAssistantNoInterpret   components.MessageKey = "lead.assistant.no_interpretation"
	msgLeadAssistantPlan          components.MessageKey = "lead.assistant.plan"
	msgLeadAssistantCompanies     components.MessageKey = "lead.assistant.companies"
	msgLeadAssistantResults       components.MessageKey = "lead.assistant.results"
	msgLeadAssistantTargets       components.MessageKey = "lead.assistant.targets"
	msgLeadAssistantReady         components.MessageKey = "lead.assistant.ready"

	msgLeadForumTitle   components.MessageKey = "lead.forum.title"
	msgLeadForumSearch  components.MessageKey = "lead.forum.search"
	msgLeadForumFind    components.MessageKey = "lead.forum.find"
	msgLeadForumByQuery components.MessageKey = "lead.forum.by_query"
	msgLeadForumEmpty   components.MessageKey = "lead.forum.empty"

	msgLeadExportTitle         components.MessageKey = "lead.export.title"
	msgLeadExportCount         components.MessageKey = "lead.export.count"
	msgLeadExportPlaceholder   components.MessageKey = "lead.export.placeholder"
	msgLeadExportSearchAria    components.MessageKey = "lead.export.search_aria"
	msgLeadExportClear         components.MessageKey = "lead.export.clear"
	msgLeadExportHeaderEmail   components.MessageKey = "lead.export.header_email"
	msgLeadExportHeaderCompany components.MessageKey = "lead.export.header_company"
	msgLeadExportHeaderLink    components.MessageKey = "lead.export.header_link"
	msgLeadExportOpenSite      components.MessageKey = "lead.export.open_site"
	msgLeadExportWrite         components.MessageKey = "lead.export.write"
	msgLeadExportEmpty         components.MessageKey = "lead.export.empty"
	msgLeadExportFooter        components.MessageKey = "lead.export.footer"

	msgLeadLiveJournal components.MessageKey = "lead.live.journal"
	msgLeadLiveCard    components.MessageKey = "lead.live.card"
	msgLeadLiveSend    components.MessageKey = "lead.live.send"
)

// consoleMessages возвращает каталог сообщений консоли лидов: базовый каталог
// общего слоя, дополненный текстами экранов.
func consoleMessages() *components.Messages {
	return components.WithMessages(components.DefaultMessages(), leadCatalog)
}

// consoleLive — область живых объявлений консоли лидов (7.3).
var consoleLive = components.Live{ID: "console-live"}
