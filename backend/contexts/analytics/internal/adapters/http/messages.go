package httpapi

import "lead-gen/frontend/components"

// Каталог сообщений консоли аналитики (web-frontend-architecture, срез 7, 7.1;
// design.md, Решение 12). Пользовательский текст шаблонов живёт здесь, а не в
// разметке: шаблон ссылается на ключ через функцию `text`, поэтому поиск по
// шаблонам не находит строковых литералов пользовательского текста. Базовые
// ключи (навигация, статусы верификации) приходят из общего слоя и не
// дублируются.
var analyticsCatalog = map[components.MessageKey]string{
	// Дашборд метрик.
	msgDashboardTitle:            "Дашборд метрик",
	msgDashboardHidden:           "Срез скрыт: данных меньше порога минимальной группы",
	msgDashboardHiddenEntries:    "записи).",
	msgDashboardProgressAria:     "Метрики прогресса",
	msgDashboardProgressTitle:    "Прогресс",
	msgDashboardFound:            "Найдено лидов",
	msgDashboardCommunicated:     "Прокоммуницировано",
	msgDashboardReplied:          "Ответили",
	msgDashboardWarmed:           "Прогретые",
	msgDashboardGrowthCaption:    "Рост по дням",
	msgDashboardGrowthEmpty:      "Нет данных для графика",
	msgDashboardGrowthAria:       "Рост найденных лидов по дням",
	msgDashboardFunnelAria:       "Воронка",
	msgDashboardFunnelTitle:      "Воронка",
	msgDashboardOperationsAria:   "Операционные метрики",
	msgDashboardOperationsTitle:  "Операции",
	msgDashboardInProgress:       "В обработке",
	msgDashboardWaiting:          "Ожидают ответа",
	msgDashboardWaitingMaxAge:    "Максимальный возраст ожидания",
	msgDashboardAverageWait:      "Среднее время ожидания",
	msgDashboardCold:             "Холодные",
	msgDashboardNotResponding:    "Не отвечают дольше",
	msgDashboardNotRespondingDay: "дней",
	msgDashboardQueue:            "В очереди на добор",

	// Отчётность.
	msgReportsTitle:              "Отчётность",
	msgReportsUpdated:            "Обновлено:",
	msgReportsFilterFrom:         "С",
	msgReportsFilterTo:           "По",
	msgReportsFilterSegment:      "Сегмент",
	msgReportsFilterSource:       "Источник",
	msgReportsFilterCampaign:     "Кампания",
	msgReportsFilterApply:        "Применить",
	msgReportsExportCSV:          "Выгрузить CSV",
	msgReportsExportXLSX:         "Выгрузить XLSX",
	msgReportsUnavailable:        "Метрики недоступны:",
	msgReportsUnavailableTail:    ". Отсутствующие значения нулём не подменяются.",
	msgReportsCampaignsAria:      "Кампании",
	msgReportsCampaignsTitle:     "Кампании за период",
	msgReportsUnapproved:         "Неутверждённых черновиков всего:",
	msgReportsHeaderCampaign:     "Кампания",
	msgReportsHeaderRecords:      "Записей",
	msgReportsHeaderSent:         "Отправлено",
	msgReportsHeaderDelivered:    "Доставлено",
	msgReportsHeaderTransitions:  "Переходы",
	msgReportsHeaderReplies:      "Ответы",
	msgReportsHeaderUnsubscribes: "Отписки",
	msgReportsHeaderBounces:      "Отказы",
	msgReportsHeaderUnapproved:   "Неутв. черновики",
	msgReportsNoPeriodData:       "Нет данных за период",
	msgReportsLeadgenAria:        "Лидогенерация",
	msgReportsLeadgenTitle:       "Лидогенерация",
	msgReportsBySources:          "По источникам",
	msgReportsByIndustries:       "По отраслям",
	msgReportsByPeriods:          "По периодам",
	msgReportsNoData:             "Нет данных",
	msgReportsFunnelAria:         "Воронка",
	msgReportsFunnelTitle:        "Воронка",
	msgReportsQualityAria:        "Качество базы",
	msgReportsQualityTitle:       "Качество базы",
	msgReportsQualityTotal:       "Всего контактов",
	msgReportsQualityValid:       "Доля валидных",
	msgReportsQualityBounce:      "Доля отказов",
	msgReportsQualityDuplicate:   "Доля дублей",
	msgReportsManagersAria:       "Управленческая отчётность",
	msgReportsManagersTitle:      "Управленческая отчётность",
	msgReportsManagersNote:       "Активность и конверсия сотрудников отделены от отчётности по лидам.",
	msgReportsHeaderManager:      "Менеджер",
	msgReportsHeaderActivity:     "Активность",
	msgReportsHeaderVolume:       "Объём",
	msgReportsHeaderDeals:        "Сделки",
	msgReportsHeaderConversion:   "Конверсия",
	msgReportsExportAsync:        "Выгрузить в фоне",
	msgReportsJobsLink:           "Задания и DLQ",

	// Ход длительной выгрузки (срез 6).
	msgExportProgressTitle: "Ход выгрузки",
	msgExportProgressLive:  "Ход выгрузки обновлён",
	msgExportJobID:         "Задание",
	msgExportJobStatus:     "Состояние",
	msgExportJobStep:       "Шаг",
	msgExportJobProgress:   "Прогресс",
	msgExportJobStarted:    "Начато",
	msgExportJobUpdated:    "Обновлено",
	msgExportJobCode:       "Код:",
	msgExportJobDownload:   "Скачать выгрузку",
	msgExportJobRetry:      "Повторить",
	msgExportJobBack:       "К отчётам",
	msgExportStatusQueued:  "в очереди",
	msgExportStatusRunning: "выполняется",
	msgExportStatusDone:    "готово",
	msgExportStatusFailed:  "отказ",
	msgExportStatusCancel:  "отменено",

	// Задания, исчерпавшие попытки (DLQ, срез 6).
	msgJobsTitle:          "Исчерпавшие попытки",
	msgJobsNote:           "Задания, которые раннер не смог довести до результата за отпущенный бюджет попыток. Повтор возвращает задание в очередь.",
	msgJobsEmpty:          "Исчерпавших попытки заданий нет",
	msgJobsCaption:        "Задания к повтору",
	msgJobsNoReason:       "Причина не сообщена",
	msgJobsHeaderID:       "Задание",
	msgJobsHeaderAttempts: "Попыток",
	msgJobsHeaderReason:   "Причина",
	msgJobsHeaderUpdated:  "Обновлено",
	msgJobsHeaderAction:   "Действие",
	msgJobsRetry:          "Повторить",
	msgJobsRetried:        "Задание возвращено в очередь:",

	// Анализ качества поиска (lead-quality-insights).
	msgInsightsTitle:               "Анализ качества поиска",
	msgInsightsUpdated:             "Обновлено:",
	msgInsightsFilterJob:           "Задание",
	msgInsightsFilterSource:        "Источник",
	msgInsightsFilterIndustry:      "Ниша",
	msgInsightsFilterFrom:          "С",
	msgInsightsFilterTo:            "По",
	msgInsightsFilterApply:         "Применить",
	msgInsightsUnavailable:         "Анализ недоступен:",
	msgInsightsVerdictsAria:        "Распределение вердиктов",
	msgInsightsVerdictsTitle:       "Вердикты",
	msgInsightsVerdictFits:         "Подходит",
	msgInsightsVerdictHypothesis:   "Гипотеза",
	msgInsightsVerdictUnsuitable:   "Не подходит",
	msgInsightsVerdictExtraction:   "Ошибки извлечения",
	msgInsightsReasonsAria:         "Причины",
	msgInsightsReasonsTitle:        "Причины «не подходит»",
	msgInsightsByJob:               "По заданиям",
	msgInsightsBySource:            "По источникам",
	msgInsightsByIndustry:          "По нишам",
	msgInsightsByPeriod:            "По периодам",
	msgInsightsRecommendationsAria: "Рекомендации",
	msgInsightsRecommendations:     "Рекомендации оператору",
	msgInsightsRecommendationsNote: "Рекомендация — основание для решения, а не гарантированный результат.",
	msgInsightsWatermark:           "Срез по watermark:",
	msgInsightsFormula:             "версия формул:",
	msgInsightsApply:               "Применить",
	msgInsightsNoData:              "Нет данных",
}

// Ключи каталога консоли аналитики.
const (
	msgDashboardTitle            components.MessageKey = "analytics.dashboard.title"
	msgDashboardHidden           components.MessageKey = "analytics.dashboard.hidden"
	msgDashboardHiddenEntries    components.MessageKey = "analytics.dashboard.hidden.entries"
	msgDashboardProgressAria     components.MessageKey = "analytics.dashboard.progress.aria"
	msgDashboardProgressTitle    components.MessageKey = "analytics.dashboard.progress.title"
	msgDashboardFound            components.MessageKey = "analytics.dashboard.metric.found"
	msgDashboardCommunicated     components.MessageKey = "analytics.dashboard.metric.communicated"
	msgDashboardReplied          components.MessageKey = "analytics.dashboard.metric.replied"
	msgDashboardWarmed           components.MessageKey = "analytics.dashboard.metric.warmed"
	msgDashboardGrowthCaption    components.MessageKey = "analytics.dashboard.growth.caption"
	msgDashboardGrowthEmpty      components.MessageKey = "analytics.dashboard.growth.empty"
	msgDashboardGrowthAria       components.MessageKey = "analytics.dashboard.growth.aria"
	msgDashboardFunnelAria       components.MessageKey = "analytics.dashboard.funnel.aria"
	msgDashboardFunnelTitle      components.MessageKey = "analytics.dashboard.funnel.title"
	msgDashboardOperationsAria   components.MessageKey = "analytics.dashboard.operations.aria"
	msgDashboardOperationsTitle  components.MessageKey = "analytics.dashboard.operations.title"
	msgDashboardInProgress       components.MessageKey = "analytics.dashboard.operation.in_progress"
	msgDashboardWaiting          components.MessageKey = "analytics.dashboard.operation.waiting"
	msgDashboardWaitingMaxAge    components.MessageKey = "analytics.dashboard.operation.waiting_max_age"
	msgDashboardAverageWait      components.MessageKey = "analytics.dashboard.operation.average_wait"
	msgDashboardCold             components.MessageKey = "analytics.dashboard.operation.cold"
	msgDashboardNotResponding    components.MessageKey = "analytics.dashboard.operation.not_responding"
	msgDashboardNotRespondingDay components.MessageKey = "analytics.dashboard.operation.not_responding.days"
	msgDashboardQueue            components.MessageKey = "analytics.dashboard.operation.queue"

	msgReportsTitle              components.MessageKey = "analytics.reports.title"
	msgReportsUpdated            components.MessageKey = "analytics.reports.updated"
	msgReportsFilterFrom         components.MessageKey = "analytics.reports.filter.from"
	msgReportsFilterTo           components.MessageKey = "analytics.reports.filter.to"
	msgReportsFilterSegment      components.MessageKey = "analytics.reports.filter.segment"
	msgReportsFilterSource       components.MessageKey = "analytics.reports.filter.source"
	msgReportsFilterCampaign     components.MessageKey = "analytics.reports.filter.campaign"
	msgReportsFilterApply        components.MessageKey = "analytics.reports.filter.apply"
	msgReportsExportCSV          components.MessageKey = "analytics.reports.export.csv"
	msgReportsExportXLSX         components.MessageKey = "analytics.reports.export.xlsx"
	msgReportsUnavailable        components.MessageKey = "analytics.reports.unavailable"
	msgReportsUnavailableTail    components.MessageKey = "analytics.reports.unavailable.tail"
	msgReportsCampaignsAria      components.MessageKey = "analytics.reports.campaigns.aria"
	msgReportsCampaignsTitle     components.MessageKey = "analytics.reports.campaigns.title"
	msgReportsUnapproved         components.MessageKey = "analytics.reports.unapproved"
	msgReportsHeaderCampaign     components.MessageKey = "analytics.reports.header.campaign"
	msgReportsHeaderRecords      components.MessageKey = "analytics.reports.header.records"
	msgReportsHeaderSent         components.MessageKey = "analytics.reports.header.sent"
	msgReportsHeaderDelivered    components.MessageKey = "analytics.reports.header.delivered"
	msgReportsHeaderTransitions  components.MessageKey = "analytics.reports.header.transitions"
	msgReportsHeaderReplies      components.MessageKey = "analytics.reports.header.replies"
	msgReportsHeaderUnsubscribes components.MessageKey = "analytics.reports.header.unsubscribes"
	msgReportsHeaderBounces      components.MessageKey = "analytics.reports.header.bounces"
	msgReportsHeaderUnapproved   components.MessageKey = "analytics.reports.header.unapproved"
	msgReportsNoPeriodData       components.MessageKey = "analytics.reports.no_period_data"
	msgReportsLeadgenAria        components.MessageKey = "analytics.reports.leadgen.aria"
	msgReportsLeadgenTitle       components.MessageKey = "analytics.reports.leadgen.title"
	msgReportsBySources          components.MessageKey = "analytics.reports.by_sources"
	msgReportsByIndustries       components.MessageKey = "analytics.reports.by_industries"
	msgReportsByPeriods          components.MessageKey = "analytics.reports.by_periods"
	msgReportsNoData             components.MessageKey = "analytics.reports.no_data"
	msgReportsFunnelAria         components.MessageKey = "analytics.reports.funnel.aria"
	msgReportsFunnelTitle        components.MessageKey = "analytics.reports.funnel.title"
	msgReportsQualityAria        components.MessageKey = "analytics.reports.quality.aria"
	msgReportsQualityTitle       components.MessageKey = "analytics.reports.quality.title"
	msgReportsQualityTotal       components.MessageKey = "analytics.reports.quality.total"
	msgReportsQualityValid       components.MessageKey = "analytics.reports.quality.valid"
	msgReportsQualityBounce      components.MessageKey = "analytics.reports.quality.bounce"
	msgReportsQualityDuplicate   components.MessageKey = "analytics.reports.quality.duplicate"
	msgReportsManagersAria       components.MessageKey = "analytics.reports.managers.aria"
	msgReportsManagersTitle      components.MessageKey = "analytics.reports.managers.title"
	msgReportsManagersNote       components.MessageKey = "analytics.reports.managers.note"
	msgReportsHeaderManager      components.MessageKey = "analytics.reports.header.manager"
	msgReportsHeaderActivity     components.MessageKey = "analytics.reports.header.activity"
	msgReportsHeaderVolume       components.MessageKey = "analytics.reports.header.volume"
	msgReportsHeaderDeals        components.MessageKey = "analytics.reports.header.deals"
	msgReportsHeaderConversion   components.MessageKey = "analytics.reports.header.conversion"
	msgReportsExportAsync        components.MessageKey = "analytics.reports.export.async"
	msgReportsJobsLink           components.MessageKey = "analytics.reports.jobs.link"

	msgExportProgressTitle components.MessageKey = "analytics.export.progress.title"
	msgExportProgressLive  components.MessageKey = "analytics.export.progress.live"
	msgExportJobID         components.MessageKey = "analytics.export.job.id"
	msgExportJobStatus     components.MessageKey = "analytics.export.job.status"
	msgExportJobStep       components.MessageKey = "analytics.export.job.step"
	msgExportJobProgress   components.MessageKey = "analytics.export.job.progress"
	msgExportJobStarted    components.MessageKey = "analytics.export.job.started"
	msgExportJobUpdated    components.MessageKey = "analytics.export.job.updated"
	msgExportJobCode       components.MessageKey = "analytics.export.job.code"
	msgExportJobDownload   components.MessageKey = "analytics.export.job.download"
	msgExportJobRetry      components.MessageKey = "analytics.export.job.retry"
	msgExportJobBack       components.MessageKey = "analytics.export.job.back"
	msgExportStatusQueued  components.MessageKey = "analytics.export.status.queued"
	msgExportStatusRunning components.MessageKey = "analytics.export.status.running"
	msgExportStatusDone    components.MessageKey = "analytics.export.status.done"
	msgExportStatusFailed  components.MessageKey = "analytics.export.status.failed"
	msgExportStatusCancel  components.MessageKey = "analytics.export.status.cancelled"

	msgJobsTitle          components.MessageKey = "analytics.jobs.title"
	msgJobsNote           components.MessageKey = "analytics.jobs.note"
	msgJobsEmpty          components.MessageKey = "analytics.jobs.empty"
	msgJobsCaption        components.MessageKey = "analytics.jobs.caption"
	msgJobsNoReason       components.MessageKey = "analytics.jobs.no_reason"
	msgJobsHeaderID       components.MessageKey = "analytics.jobs.header.id"
	msgJobsHeaderAttempts components.MessageKey = "analytics.jobs.header.attempts"
	msgJobsHeaderReason   components.MessageKey = "analytics.jobs.header.reason"
	msgJobsHeaderUpdated  components.MessageKey = "analytics.jobs.header.updated"
	msgJobsHeaderAction   components.MessageKey = "analytics.jobs.header.action"
	msgJobsRetry          components.MessageKey = "analytics.jobs.retry"
	msgJobsRetried        components.MessageKey = "analytics.jobs.retried"

	// Анализ качества поиска (lead-quality-insights).
	msgInsightsTitle               components.MessageKey = "analytics.insights.title"
	msgInsightsUpdated             components.MessageKey = "analytics.insights.updated"
	msgInsightsFilterJob           components.MessageKey = "analytics.insights.filter.job"
	msgInsightsFilterSource        components.MessageKey = "analytics.insights.filter.source"
	msgInsightsFilterIndustry      components.MessageKey = "analytics.insights.filter.industry"
	msgInsightsFilterFrom          components.MessageKey = "analytics.insights.filter.from"
	msgInsightsFilterTo            components.MessageKey = "analytics.insights.filter.to"
	msgInsightsFilterApply         components.MessageKey = "analytics.insights.filter.apply"
	msgInsightsUnavailable         components.MessageKey = "analytics.insights.unavailable"
	msgInsightsVerdictsAria        components.MessageKey = "analytics.insights.verdicts.aria"
	msgInsightsVerdictsTitle       components.MessageKey = "analytics.insights.verdicts.title"
	msgInsightsVerdictFits         components.MessageKey = "analytics.insights.verdict.fits"
	msgInsightsVerdictHypothesis   components.MessageKey = "analytics.insights.verdict.hypothesis"
	msgInsightsVerdictUnsuitable   components.MessageKey = "analytics.insights.verdict.unsuitable"
	msgInsightsVerdictExtraction   components.MessageKey = "analytics.insights.verdict.extraction"
	msgInsightsReasonsAria         components.MessageKey = "analytics.insights.reasons.aria"
	msgInsightsReasonsTitle        components.MessageKey = "analytics.insights.reasons.title"
	msgInsightsByJob               components.MessageKey = "analytics.insights.by_job"
	msgInsightsBySource            components.MessageKey = "analytics.insights.by_source"
	msgInsightsByIndustry          components.MessageKey = "analytics.insights.by_industry"
	msgInsightsByPeriod            components.MessageKey = "analytics.insights.by_period"
	msgInsightsRecommendationsAria components.MessageKey = "analytics.insights.recommendations.aria"
	msgInsightsRecommendations     components.MessageKey = "analytics.insights.recommendations.title"
	msgInsightsRecommendationsNote components.MessageKey = "analytics.insights.recommendations.note"
	msgInsightsWatermark           components.MessageKey = "analytics.insights.watermark"
	msgInsightsFormula             components.MessageKey = "analytics.insights.formula"
	msgInsightsApply               components.MessageKey = "analytics.insights.apply"
	msgInsightsNoData              components.MessageKey = "analytics.insights.no_data"
)

// consoleMessages возвращает каталог сообщений консоли аналитики: базовый
// каталог общего слоя, дополненный текстами экранов дашборда и отчётности.
func consoleMessages() *components.Messages {
	return components.WithMessages(components.DefaultMessages(), analyticsCatalog)
}
