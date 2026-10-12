package httpapi

import "lead-gen/frontend/components"

// Каталог сообщений консоли, портала и топологии platform-governance.
//
// Пользовательский текст экранов живёт здесь, а не в шаблонах
// (web-frontend-architecture, срез 7, 7.1): шаблон ссылается на ключ через
// функцию `text`, поэтому поиск по шаблонам не находит кириллицы. Каталог
// расширяет общий каталог frontend/components через WithMessages: общие ключи
// (навигация, aria-live, каркас) остаются, ключи контекста добавляются.
//
// Ключи машинные (ASCII), значения — русский текст. Значения без подстановок,
// чтобы шаблон оставался без логики форматирования: даты и числа проходит
// Formatter общего слоя (formatDateTime и т.п.).

const (
	// Портал клиента и регистрация.
	msgPortalRegisterTitle  components.MessageKey = "portal.register.title"
	msgPortalRegisterSubmit components.MessageKey = "portal.register.submit"
	msgPortalTitle          components.MessageKey = "portal.title"
	msgPortalRequestID      components.MessageKey = "portal.request.id"
	msgPortalRequestStatus  components.MessageKey = "portal.request.status"
	msgPortalRequestEmpty   components.MessageKey = "portal.request.empty"
	msgPortalRequestSubmit  components.MessageKey = "portal.request.submit"
	msgOfferTitle           components.MessageKey = "portal.offer.title"
	msgOfferSubmit          components.MessageKey = "portal.offer.submit"

	// Вход, подтверждение регистрации, профиль и метрики портала.
	msgLoginTitle                components.MessageKey = "portal.login.title"
	msgLoginSubmit               components.MessageKey = "portal.login.submit"
	msgLoginReset                components.MessageKey = "portal.login.reset"
	msgLoginNoAccount            components.MessageKey = "portal.login.no_account"
	msgAuthLogin                 components.MessageKey = "portal.auth.login"
	msgAuthRegister              components.MessageKey = "portal.auth.register"
	msgRegisterHaveAccount       components.MessageKey = "portal.register.have_account"
	msgRegisterSectionCompany    components.MessageKey = "portal.register.section.company"
	msgRegisterSectionAccount    components.MessageKey = "portal.register.section.account"
	msgRegisterSectionLegal      components.MessageKey = "portal.register.section.legal"
	msgResetBackToLogin          components.MessageKey = "portal.reset.back_to_login"
	msgRegisterSentTitle         components.MessageKey = "portal.register.sent.title"
	msgRegisterSentBody          components.MessageKey = "portal.register.sent.body"
	msgRegisterSentBackToLogin   components.MessageKey = "portal.register.sent.back_to_login"
	msgProfileTitle              components.MessageKey = "portal.profile.title"
	msgProfilePending            components.MessageKey = "portal.profile.pending"
	msgProfileDescription        components.MessageKey = "portal.profile.description"
	msgProfileTarget             components.MessageKey = "portal.profile.target"
	msgProfileServices           components.MessageKey = "portal.profile.services"
	msgProfileServiceName        components.MessageKey = "portal.profile.service.name"
	msgProfileServiceShort       components.MessageKey = "portal.profile.service.short"
	msgProfileServiceFull        components.MessageKey = "portal.profile.service.full"
	msgProfileSave               components.MessageKey = "portal.profile.save"
	msgProfileSubmit             components.MessageKey = "portal.profile.submit"
	msgPortalServices            components.MessageKey = "portal.services"
	msgPortalServicesEmpty       components.MessageKey = "portal.services.empty"
	msgPortalMetricsTitle        components.MessageKey = "portal.metrics.title"
	msgPortalMetricsFound        components.MessageKey = "portal.metrics.found"
	msgPortalMetricsCommunicated components.MessageKey = "portal.metrics.communicated"
	msgPortalMetricsReplied      components.MessageKey = "portal.metrics.replied"
	msgPortalRequestsTitle       components.MessageKey = "portal.requests.title"
	msgPortalRequestsEmpty       components.MessageKey = "portal.requests.empty"
	msgPortalRequestsUnavailable components.MessageKey = "portal.requests.unavailable"
	msgResetTitle                components.MessageKey = "portal.reset.title"
	msgResetHint                 components.MessageKey = "portal.reset.hint"
	msgResetSubmit               components.MessageKey = "portal.reset.submit"

	// Раздел «Тенант»: список и выбор активного.
	msgConsoleTenantTitle     components.MessageKey = "console.tenant.title"
	msgConsoleTenantActive    components.MessageKey = "console.tenant.active"
	msgConsoleTenantColID     components.MessageKey = "console.tenant.col.id"
	msgConsoleTenantColComp   components.MessageKey = "console.tenant.col.company"
	msgConsoleTenantColState  components.MessageKey = "console.tenant.col.state"
	msgConsoleTenantColAction components.MessageKey = "console.tenant.col.action"
	msgConsoleTenantSwitch    components.MessageKey = "console.tenant.switch"
	msgConsoleTenantCurrent   components.MessageKey = "console.tenant.current"
	msgConsoleTenantEmpty     components.MessageKey = "console.tenant.empty"

	// Раздел «Пользователи и роли».
	msgConsoleUsersTitle    components.MessageKey = "console.users.title"
	msgConsoleTenantLabel   components.MessageKey = "console.tenant.label"
	msgConsoleUsersColUser  components.MessageKey = "console.users.col.user"
	msgConsoleUsersColLogin components.MessageKey = "console.users.col.login"
	msgConsoleUsersColRoles components.MessageKey = "console.users.col.roles"
	msgConsoleUsersColAct   components.MessageKey = "console.users.col.action"
	msgConsoleUsersRevoke   components.MessageKey = "console.users.revoke"
	msgConsoleUsersAssignH  components.MessageKey = "console.users.assign.heading"
	msgConsoleUsersRoleLbl  components.MessageKey = "console.users.role.label"
	msgConsoleUsersAssign   components.MessageKey = "console.users.assign"
	msgConsoleUsersEmpty    components.MessageKey = "console.users.empty"
	msgConsoleRoleAdmin     components.MessageKey = "console.users.role.admin"
	msgConsoleRoleClient    components.MessageKey = "console.users.role.client"

	// Раздел «Интеграции и адаптеры».
	msgConsoleIntegrationsTitle components.MessageKey = "console.integrations.title"
	msgConsoleIntegrationsDown  components.MessageKey = "console.integrations.unavailable"
	msgConsoleIntegrationsAdapt components.MessageKey = "console.integrations.col.adapter"
	msgConsoleIntegrationsState components.MessageKey = "console.integrations.col.state"
	msgConsoleIntegrationsWhy   components.MessageKey = "console.integrations.col.reason"
	msgConsoleIntegrationsEmpty components.MessageKey = "console.integrations.empty"

	// Раздел «Журнал аудита».
	msgConsoleAuditTitle     components.MessageKey = "console.audit.title"
	msgConsoleAuditImmutable components.MessageKey = "console.audit.immutable"
	msgConsoleAuditExport    components.MessageKey = "console.audit.export"
	msgConsoleAuditColAt     components.MessageKey = "console.audit.col.at"
	msgConsoleAuditColActor  components.MessageKey = "console.audit.col.actor"
	msgConsoleAuditColAction components.MessageKey = "console.audit.col.action"
	msgConsoleAuditColObject components.MessageKey = "console.audit.col.object"
	msgConsoleAuditColOut    components.MessageKey = "console.audit.col.outcome"
	msgConsoleAuditColRec    components.MessageKey = "console.audit.col.record"
	msgConsoleAuditCorrect   components.MessageKey = "console.audit.correction"
	msgConsoleAuditEmpty     components.MessageKey = "console.audit.empty"

	// Навигация раздела управления.
	msgConsoleNavTenant        components.MessageKey = "console.nav.tenant"
	msgConsoleNavUsers         components.MessageKey = "console.nav.users"
	msgConsoleNavIntegrations  components.MessageKey = "console.nav.integrations"
	msgConsoleNavAudit         components.MessageKey = "console.nav.audit"
	msgConsoleNavTopology      components.MessageKey = "console.nav.topology"
	msgConsoleNavOperations    components.MessageKey = "console.nav.operations"
	msgConsoleNavRegistrations components.MessageKey = "console.nav.registrations"

	// Вход оператора консоли платформы.
	msgConsoleLoginTitle    components.MessageKey = "console.login.title"
	msgConsoleLoginUsername components.MessageKey = "console.login.username"
	msgConsoleLoginPassword components.MessageKey = "console.login.password"
	msgConsoleLoginSubmit   components.MessageKey = "console.login.submit"

	// Раздел «Регистрации»: заявки на регистрацию и профили на согласовании.
	msgConsoleRegistrationsTitle      components.MessageKey = "console.registrations.title"
	msgConsoleRegistrationsEmpty      components.MessageKey = "console.registrations.empty"
	msgConsoleRegistrationsColCompany components.MessageKey = "console.registrations.col.company"
	msgConsoleRegistrationsColDesc    components.MessageKey = "console.registrations.col.description"
	msgConsoleRegistrationsColINN     components.MessageKey = "console.registrations.col.inn"
	msgConsoleRegistrationsColEmail   components.MessageKey = "console.registrations.col.email"
	msgConsoleRegistrationsColWebsite components.MessageKey = "console.registrations.col.website"
	msgConsoleRegistrationsColLegal   components.MessageKey = "console.registrations.col.legal"
	msgConsoleRegistrationsColCreated components.MessageKey = "console.registrations.col.created"
	msgConsoleRegistrationsApprove    components.MessageKey = "console.registrations.approve"
	msgConsoleRegistrationsReject     components.MessageKey = "console.registrations.reject"
	msgConsoleRegistrationsReason     components.MessageKey = "console.registrations.reason"
	msgConsoleProfilesTitle           components.MessageKey = "console.profiles.title"
	msgConsoleProfilesEmpty           components.MessageKey = "console.profiles.empty"
	msgConsoleProfilesUnavailable     components.MessageKey = "console.profiles.unavailable"
	msgConsoleProfilesDescription     components.MessageKey = "console.profiles.description"
	msgConsoleProfilesTarget          components.MessageKey = "console.profiles.target"
	msgConsoleProfilesServices        components.MessageKey = "console.profiles.services"
	msgConsoleProfilesApprove         components.MessageKey = "console.profiles.approve"
	msgConsoleProfilesReject          components.MessageKey = "console.profiles.reject"

	// Экран топологии.
	msgTopologyTitle      components.MessageKey = "topology.title"
	msgTopologyGenerated  components.MessageKey = "topology.generated_at"
	msgTopologyUnavail    components.MessageKey = "topology.unavailable"
	msgTopologyWarnings   components.MessageKey = "topology.warnings"
	msgTopologyGraphAria  components.MessageKey = "topology.graph.aria"
	msgTopologyNodesHead  components.MessageKey = "topology.nodes.heading"
	msgTopologyEdgesHead  components.MessageKey = "topology.edges.heading"
	msgTopologyEmpty      components.MessageKey = "topology.empty"
	msgTopologyColService components.MessageKey = "topology.col.service"
	msgTopologyColKind    components.MessageKey = "topology.col.kind"
	msgTopologyColZone    components.MessageKey = "topology.col.zone"
	msgTopologyColRoles   components.MessageKey = "topology.col.roles"
	msgTopologyColState   components.MessageKey = "topology.col.state"
	msgTopologyColConns   components.MessageKey = "topology.col.connections"
	msgTopologyColContr   components.MessageKey = "topology.col.contracts"
	msgTopologyColEdge    components.MessageKey = "topology.col.edge"
	msgTopologyColFrom    components.MessageKey = "topology.col.from"
	msgTopologyColTo      components.MessageKey = "topology.col.to"
	msgTopologyColIntegr  components.MessageKey = "topology.col.integration"
	msgTopologyColTransp  components.MessageKey = "topology.col.transport"
	msgTopologyColMech    components.MessageKey = "topology.col.mechanism"
	msgTopologyColContrct components.MessageKey = "topology.col.contract"

	// Карточка узла.
	msgTopologyNodeTitle    components.MessageKey = "topology.node.title"
	msgTopologyNodeMissing  components.MessageKey = "topology.node.not_found"
	msgTopologyNodeID       components.MessageKey = "topology.node.id"
	msgTopologyNodeKind     components.MessageKey = "topology.node.kind"
	msgTopologyNodeZone     components.MessageKey = "topology.node.zone"
	msgTopologyNodeRoles    components.MessageKey = "topology.node.roles"
	msgTopologyNodeGroups   components.MessageKey = "topology.node.groups"
	msgTopologyNodeAddr     components.MessageKey = "topology.node.addresses"
	msgTopologyNodeState    components.MessageKey = "topology.node.state"
	msgTopologyNodeContract components.MessageKey = "topology.node.contracts"
	msgTopologyNodeConns    components.MessageKey = "topology.node.connections"
	msgTopologyNodeBack     components.MessageKey = "topology.node.back"
	msgTopologyNodeEmpty    components.MessageKey = "topology.node.empty"
	msgTopologyNodeLoaded   components.MessageKey = "topology.node.loaded"

	// Виды узлов и зоны данных.
	msgTopologyKindService components.MessageKey = "topology.kind.service"
	msgTopologyKindInfra   components.MessageKey = "topology.kind.infrastructure"
	msgTopologyKindUnknown components.MessageKey = "topology.kind.unknown"
	msgTopologyZoneGreen   components.MessageKey = "topology.zone.green"
	msgTopologyZoneRed     components.MessageKey = "topology.zone.red"
	msgTopologyZoneMixed   components.MessageKey = "topology.zone.mixed"
	msgTopologyZoneInfra   components.MessageKey = "topology.zone.infra"

	// Состояние узла: подпись и значок — смысл не передаётся только цветом (7.4).
	msgTopologyStateUp       components.MessageKey = "topology.state.up"
	msgTopologyStateDegraded components.MessageKey = "topology.state.degraded"
	msgTopologyStateDown     components.MessageKey = "topology.state.down"
	msgTopologyStateUnknown  components.MessageKey = "topology.state.unknown"
	msgTopologyStateIconUp   components.MessageKey = "topology.state.icon.up"
	msgTopologyStateIconDeg  components.MessageKey = "topology.state.icon.degraded"
	msgTopologyStateIconDown components.MessageKey = "topology.state.icon.down"
	msgTopologyStateIconUnk  components.MessageKey = "topology.state.icon.unknown"

	// Интенсивность ребра.
	msgTopologyIntensityUnknown components.MessageKey = "topology.intensity.unknown"
	msgTopologyIntensityNormal  components.MessageKey = "topology.intensity.normal"
	msgTopologyIntensityHot     components.MessageKey = "topology.intensity.hot"

	// Объявленные панели метрик: подписи.
	msgTopologyPanelReqRate components.MessageKey = "topology.panel.request_rate"
	msgTopologyPanelErrRate components.MessageKey = "topology.panel.error_ratio"

	// Метрики: watermark и подписи исходов.
	msgTopologyMetricsUnavail components.MessageKey = "topology.metrics.unavailable"
	msgTopologyMetricsNoData  components.MessageKey = "topology.metrics.no_data"

	// Экран операций: заголовки и навигация.
	msgCockpitNavOperations               components.MessageKey = "cockpit.nav.operations"
	msgCockpitTitleOperations             components.MessageKey = "cockpit.title.operations"
	msgCockpitTitleAgents                 components.MessageKey = "cockpit.title.agents"
	msgCockpitTitleAgent                  components.MessageKey = "cockpit.title.agent"
	msgCockpitTitleProcess                components.MessageKey = "cockpit.title.process"
	msgCockpitTitleDashboard              components.MessageKey = "cockpit.title.dashboard"
	msgCockpitRequestsHeading             components.MessageKey = "cockpit.requests.heading"
	msgCockpitRequestsUnavailable         components.MessageKey = "cockpit.requests.unavailable"
	msgCockpitRequestsEmpty               components.MessageKey = "cockpit.requests.empty"
	msgCockpitRequestColID                components.MessageKey = "cockpit.requests.col.id"
	msgCockpitRequestColStatus            components.MessageKey = "cockpit.requests.col.status"
	msgCockpitRequestColPhase             components.MessageKey = "cockpit.requests.col.phase"
	msgCockpitRequestColWorkflow          components.MessageKey = "cockpit.requests.col.workflow"
	msgCockpitRequestColCounts            components.MessageKey = "cockpit.requests.col.counts"
	msgCockpitRequestExecutionUnavailable components.MessageKey = "cockpit.requests.execution_unavailable"
	msgCockpitRequestCreated              components.MessageKey = "cockpit.requests.status.created"
	msgCockpitRequestProcessing           components.MessageKey = "cockpit.requests.status.processing"
	msgCockpitRequestCancelled            components.MessageKey = "cockpit.requests.status.cancelled"
	msgCockpitRequestFulfilled            components.MessageKey = "cockpit.requests.status.fulfilled"
	msgCockpitRequestRejected             components.MessageKey = "cockpit.requests.status.rejected"
	msgCockpitRequestNeedsRevision        components.MessageKey = "cockpit.requests.status.needs_revision"
	msgCockpitRequestFailed               components.MessageKey = "cockpit.requests.status.failed"

	// Цифры процесса (6.1).
	msgCockpitColCategory            components.MessageKey = "cockpit.col.category"
	msgCockpitColClient              components.MessageKey = "cockpit.col.client"
	msgCockpitColTotal               components.MessageKey = "cockpit.col.total"
	msgCockpitColDate                components.MessageKey = "cockpit.col.date"
	msgCockpitNumbersEmpty           components.MessageKey = "cockpit.numbers.empty"
	msgCockpitNumbersUnavailable     components.MessageKey = "cockpit.numbers.unavailable"
	msgCockpitNumbersPeriods         components.MessageKey = "cockpit.numbers.periods"
	msgCockpitCategoryFound          components.MessageKey = "cockpit.category.found"
	msgCockpitCategoryProcessed      components.MessageKey = "cockpit.category.processed"
	msgCockpitCategoryRejected       components.MessageKey = "cockpit.category.rejected"
	msgCockpitCategoryInProgress     components.MessageKey = "cockpit.category.in_progress"
	msgCockpitCategoryResult         components.MessageKey = "cockpit.category.result"
	msgCockpitCategoryIconFound      components.MessageKey = "cockpit.category.icon.found"
	msgCockpitCategoryIconProcessed  components.MessageKey = "cockpit.category.icon.processed"
	msgCockpitCategoryIconRejected   components.MessageKey = "cockpit.category.icon.rejected"
	msgCockpitCategoryIconInProgress components.MessageKey = "cockpit.category.icon.in_progress"
	msgCockpitCategoryIconResult     components.MessageKey = "cockpit.category.icon.result"

	// Состояния агента и исполнения: подпись и маркер (смысл не только цветом).
	msgCockpitStateRunning              components.MessageKey = "cockpit.state.running"
	msgCockpitStateCompleted            components.MessageKey = "cockpit.state.completed"
	msgCockpitStateFailed               components.MessageKey = "cockpit.state.failed"
	msgCockpitStateQueued               components.MessageKey = "cockpit.state.queued"
	msgCockpitStateWaiting              components.MessageKey = "cockpit.state.waiting"
	msgCockpitStateUnknown              components.MessageKey = "cockpit.state.unknown"
	msgCockpitStateAwaitingApproval     components.MessageKey = "cockpit.state.awaiting_approval"
	msgCockpitStateBlocked              components.MessageKey = "cockpit.state.blocked"
	msgCockpitStateNeedsAttention       components.MessageKey = "cockpit.state.needs_attention"
	msgCockpitStateStopped              components.MessageKey = "cockpit.state.stopped"
	msgCockpitStateIconRunning          components.MessageKey = "cockpit.state.icon.running"
	msgCockpitStateIconCompleted        components.MessageKey = "cockpit.state.icon.completed"
	msgCockpitStateIconFailed           components.MessageKey = "cockpit.state.icon.failed"
	msgCockpitStateIconQueued           components.MessageKey = "cockpit.state.icon.queued"
	msgCockpitStateIconUnknown          components.MessageKey = "cockpit.state.icon.unknown"
	msgCockpitStateIconAwaitingApproval components.MessageKey = "cockpit.state.icon.awaiting_approval"
	msgCockpitStateIconStopped          components.MessageKey = "cockpit.state.icon.stopped"
	msgCockpitProcessCanceled           components.MessageKey = "cockpit.process.canceled"
	msgCockpitProcessTerminated         components.MessageKey = "cockpit.process.terminated"
	msgCockpitProcessContinued          components.MessageKey = "cockpit.process.continued"
	msgCockpitProcessTimedOut           components.MessageKey = "cockpit.process.timed_out"

	// Шаги и ожидания.
	msgCockpitKindActivity       components.MessageKey = "cockpit.kind.activity"
	msgCockpitKindTimer          components.MessageKey = "cockpit.kind.timer"
	msgCockpitKindSignal         components.MessageKey = "cockpit.kind.signal"
	msgCockpitOutcomeScheduled   components.MessageKey = "cockpit.outcome.scheduled"
	msgCockpitOutcomeCompleted   components.MessageKey = "cockpit.outcome.completed"
	msgCockpitOutcomeFailed      components.MessageKey = "cockpit.outcome.failed"
	msgCockpitOutcomeTimedOut    components.MessageKey = "cockpit.outcome.timed_out"
	msgCockpitOutcomeCanceled    components.MessageKey = "cockpit.outcome.canceled"
	msgCockpitOutcomeUnavailable components.MessageKey = "cockpit.outcome.unavailable"
	msgCockpitStateWaitStarted   components.MessageKey = "cockpit.wait.started"
	msgCockpitStateWaitFired     components.MessageKey = "cockpit.wait.fired"
	msgCockpitStateWaitReceived  components.MessageKey = "cockpit.wait.received"

	// Живая лента агентов (6.2).
	msgCockpitColRun            components.MessageKey = "cockpit.col.run"
	msgCockpitColCompany        components.MessageKey = "cockpit.col.company"
	msgCockpitColState          components.MessageKey = "cockpit.col.state"
	msgCockpitColStep           components.MessageKey = "cockpit.col.step"
	msgCockpitColUpdated        components.MessageKey = "cockpit.col.updated"
	msgCockpitAgentsEmpty       components.MessageKey = "cockpit.agents.empty"
	msgCockpitAgentsUnavailable components.MessageKey = "cockpit.agents.unavailable"
	msgCockpitAgentsAria        components.MessageKey = "cockpit.agents.aria"
	msgCockpitAgentsLive        components.MessageKey = "cockpit.agents.live"
	msgCockpitAgentLink         components.MessageKey = "cockpit.agents.link"
	msgCockpitAgentsExpand      components.MessageKey = "cockpit.agents.expand"

	// Подробности агента/шага (6.3).
	msgCockpitAgentUnavailable components.MessageKey = "cockpit.agent.unavailable"
	msgCockpitAgentMissing     components.MessageKey = "cockpit.agent.missing"
	msgCockpitAgentEvents      components.MessageKey = "cockpit.agent.events"
	msgCockpitAgentReasons     components.MessageKey = "cockpit.agent.reasons"
	msgCockpitAgentJobs        components.MessageKey = "cockpit.agent.jobs"
	msgCockpitAgentNoEvents    components.MessageKey = "cockpit.agent.no_events"
	msgCockpitAgentNoReasons   components.MessageKey = "cockpit.agent.no_reasons"
	msgCockpitAgentNoJobs      components.MessageKey = "cockpit.agent.no_jobs"
	msgCockpitAgentBack        components.MessageKey = "cockpit.agent.back"
	msgCockpitColTime          components.MessageKey = "cockpit.col.time"
	msgCockpitColOutcome       components.MessageKey = "cockpit.col.outcome"
	msgCockpitColReason        components.MessageKey = "cockpit.col.reason"
	msgCockpitColCount         components.MessageKey = "cockpit.col.count"
	msgCockpitColJob           components.MessageKey = "cockpit.col.job"
	msgCockpitColKind          components.MessageKey = "cockpit.col.kind"

	// Граф процессов Temporal (6.4).
	msgCockpitProcessUnavailable  components.MessageKey = "cockpit.process.unavailable"
	msgCockpitProcessEmpty        components.MessageKey = "cockpit.process.empty"
	msgCockpitProcessAria         components.MessageKey = "cockpit.process.graph.aria"
	msgCockpitProcessLive         components.MessageKey = "cockpit.process.graph.live"
	msgCockpitProcessTimeline     components.MessageKey = "cockpit.process.timeline"
	msgCockpitProcessTimelineAria components.MessageKey = "cockpit.process.timeline.aria"
	msgCockpitProcessSelect       components.MessageKey = "cockpit.process.select"
	msgCockpitColWorkflow         components.MessageKey = "cockpit.col.workflow"
	msgCockpitColType             components.MessageKey = "cockpit.col.type"
	msgCockpitColStarted          components.MessageKey = "cockpit.col.started"

	// Полноэкранный дашборд по клиентам (6.5).
	msgCockpitDashboardUnavailable components.MessageKey = "cockpit.dashboard.unavailable"
	msgCockpitDashboardThreshold   components.MessageKey = "cockpit.dashboard.threshold"
	msgCockpitDashboardEmpty       components.MessageKey = "cockpit.dashboard.empty"
	msgCockpitColAnomalies         components.MessageKey = "cockpit.col.anomalies"

	// Расширяемые панели (5.4).
	msgCockpitPanelsHeading        components.MessageKey = "cockpit.panels.heading"
	msgCockpitPanelSource          components.MessageKey = "cockpit.panel.source"
	msgCockpitPanelRefresh         components.MessageKey = "cockpit.panel.refresh"
	msgCockpitPanelUnavailable     components.MessageKey = "cockpit.panel.unavailable"
	msgCockpitPanelNoData          components.MessageKey = "cockpit.panel.no_data"
	msgCockpitPanelHidden          components.MessageKey = "cockpit.panel.hidden"
	msgCockpitPanelHide            components.MessageKey = "cockpit.panel.hide"
	msgCockpitPanelShow            components.MessageKey = "cockpit.panel.show"
	msgCockpitSourceMetrics        components.MessageKey = "cockpit.source.metrics"
	msgCockpitSourceProcess        components.MessageKey = "cockpit.source.process"
	msgCockpitSourceAggregate      components.MessageKey = "cockpit.source.aggregate"
	msgCockpitRefreshPoll          components.MessageKey = "cockpit.refresh.poll"
	msgCockpitRefreshStatic        components.MessageKey = "cockpit.refresh.static"
	msgCockpitPanelProcess         components.MessageKey = "cockpit.panel.process"
	msgCockpitPanelWorkflowStarted components.MessageKey = "cockpit.panel.workflow_started"
	msgCockpitPanelWorkflowErrors  components.MessageKey = "cockpit.panel.workflow_errors"
	msgCockpitPanelHTTPErrorRatio  components.MessageKey = "cockpit.panel.http_error_ratio"

	// Нормализованные причины отказов.
	msgCockpitReasonNone                components.MessageKey = "cockpit.reason.none"
	msgCockpitReasonNotRelevant         components.MessageKey = "cockpit.reason.not_relevant"
	msgCockpitReasonNoContact           components.MessageKey = "cockpit.reason.no_contact"
	msgCockpitReasonExtractionError     components.MessageKey = "cockpit.reason.extraction_error"
	msgCockpitReasonProviderUnavailable components.MessageKey = "cockpit.reason.provider_unavailable"

	msgCockpitReasonTimeout components.MessageKey = "cockpit.reason.timeout"

	// Аномалии дашборда.
	msgCockpitAnomalyStalled        components.MessageKey = "cockpit.anomaly.stalled"
	msgCockpitAnomalyUnknown        components.MessageKey = "cockpit.anomaly.unknown"
	msgCockpitAnomalyRejectionSpike components.MessageKey = "cockpit.anomaly.rejection_spike"

	// Уведомления и отказы экрана операций.
	msgCockpitNoticeBadRequest        components.MessageKey = "cockpit.notice.bad_request"
	msgCockpitNoticeCSRF              components.MessageKey = "cockpit.notice.csrf"
	msgCockpitNoticeBadPanel          components.MessageKey = "cockpit.notice.bad_panel"
	msgCockpitNoticePanelsUnavailable components.MessageKey = "cockpit.notice.panels_unavailable"
)

// consoleCatalog — значения каталога контекста. Единственный источник
// пользовательского текста экранов.
var consoleCatalog = map[components.MessageKey]string{
	msgPortalRegisterTitle:  "Регистрация клиента",
	msgPortalRegisterSubmit: "Зарегистрироваться",
	msgPortalTitle:          "Портал клиента",
	msgPortalRequestID:      "Заявка",
	msgPortalRequestStatus:  "Статус",
	msgPortalRequestEmpty:   "Заявка ещё не подана.",
	msgPortalRequestSubmit:  "Подать заявку",
	msgOfferTitle:           "Заявка",
	msgOfferSubmit:          "Отправить заявку",

	msgLoginTitle:                "Вход",
	msgLoginSubmit:               "Войти",
	msgLoginReset:                "Забыли пароль?",
	msgLoginNoAccount:            "Нет аккаунта?",
	msgAuthLogin:                 "Вход",
	msgAuthRegister:              "Регистрация",
	msgRegisterHaveAccount:       "Уже есть аккаунт?",
	msgRegisterSectionCompany:    "Компания",
	msgRegisterSectionAccount:    "Учётная запись",
	msgRegisterSectionLegal:      "Реквизиты компании",
	msgResetBackToLogin:          "Вернуться ко входу",
	msgRegisterSentTitle:         "Заявка на регистрацию отправлена",
	msgRegisterSentBody:          "Мы получили вашу заявку. После проверки администратором вы сможете войти в портал.",
	msgRegisterSentBackToLogin:   "На страницу входа",
	msgProfileTitle:              "Профиль компании",
	msgProfilePending:            "Профиль на согласовании: правка недоступна.",
	msgProfileDescription:        "Описание компании",
	msgProfileTarget:             "Кого ищете",
	msgProfileServices:           "Услуги",
	msgProfileServiceName:        "Название услуги",
	msgProfileServiceShort:       "Краткое описание",
	msgProfileServiceFull:        "Полное описание",
	msgProfileSave:               "Сохранить",
	msgProfileSubmit:             "Отправить на согласование",
	msgPortalServices:            "Услуги компании",
	msgPortalServicesEmpty:       "Услуги пока не добавлены.",
	msgPortalMetricsTitle:        "Метрики",
	msgPortalMetricsFound:        "Найдено лидов",
	msgPortalMetricsCommunicated: "Взаимодействий",
	msgPortalMetricsReplied:      "Откликов",
	msgPortalRequestsTitle:       "История заявок",
	msgPortalRequestsEmpty:       "Заявок пока нет.",
	msgPortalRequestsUnavailable: "История заявок недоступна.",
	msgResetTitle:                "Сброс пароля",
	msgResetHint:                 "Запрос на сброс пароля будет передан администратору.",
	msgResetSubmit:               "Отправить запрос",

	msgConsoleUsersTitle:  "Пользователи и роли",
	msgConsoleTenantLabel: "Тенант",

	msgConsoleTenantTitle:     "Выбор тенанта",
	msgConsoleTenantActive:    "Активный тенант",
	msgConsoleTenantColID:     "Идентификатор",
	msgConsoleTenantColComp:   "Компания",
	msgConsoleTenantColState:  "Состояние",
	msgConsoleTenantColAction: "Действие",
	msgConsoleTenantSwitch:    "Выбрать",
	msgConsoleTenantCurrent:   "Текущий",
	msgConsoleTenantEmpty:     "Тенантов пока нет.",
	msgConsoleUsersColUser:    "Пользователь",
	msgConsoleUsersColLogin:   "Логин",
	msgConsoleUsersColRoles:   "Роли",
	msgConsoleUsersColAct:     "Действие",
	msgConsoleUsersRevoke:     "Отозвать",
	msgConsoleUsersAssignH:    "Назначить роль",
	msgConsoleUsersRoleLbl:    "Роль",
	msgConsoleUsersAssign:     "Назначить",
	msgConsoleUsersEmpty:      "Пользователей пока нет.",
	msgConsoleRoleAdmin:       "Администратор платформы",
	msgConsoleRoleClient:      "Клиент",

	msgConsoleIntegrationsTitle: "Интеграции и адаптеры",
	msgConsoleIntegrationsDown:  "Состояние интеграций недоступно",
	msgConsoleIntegrationsAdapt: "Адаптер",
	msgConsoleIntegrationsState: "Состояние",
	msgConsoleIntegrationsWhy:   "Причина",
	msgConsoleIntegrationsEmpty: "Адаптеры не объявлены.",

	msgConsoleAuditTitle:     "Журнал аудита",
	msgConsoleAuditImmutable: "Журнал неизменяем: записи нельзя изменить или удалить, корректировка добавляет новую запись.",
	msgConsoleAuditExport:    "Выгрузить журнал (CSV)",
	msgConsoleAuditColAt:     "Время",
	msgConsoleAuditColActor:  "Субъект",
	msgConsoleAuditColAction: "Действие",
	msgConsoleAuditColObject: "Объект",
	msgConsoleAuditColOut:    "Результат",
	msgConsoleAuditColRec:    "Запись",
	msgConsoleAuditCorrect:   "корректировка",
	msgConsoleAuditEmpty:     "Записей пока нет.",

	msgConsoleNavUsers:         "Пользователи",
	msgConsoleNavTenant:        "Тенант",
	msgConsoleNavIntegrations:  "Интеграции",
	msgConsoleNavAudit:         "Аудит",
	msgConsoleNavTopology:      "Топология",
	msgConsoleNavOperations:    "Операции",
	msgConsoleNavRegistrations: "Регистрации",

	msgConsoleLoginTitle:    "Вход в консоль",
	msgConsoleLoginUsername: "Логин",
	msgConsoleLoginPassword: "Пароль",
	msgConsoleLoginSubmit:   "Войти",

	msgConsoleRegistrationsTitle:      "Заявки на регистрацию",
	msgConsoleRegistrationsEmpty:      "Заявок на рассмотрении нет.",
	msgConsoleRegistrationsColCompany: "Компания",
	msgConsoleRegistrationsColDesc:    "Описание",
	msgConsoleRegistrationsColINN:     "ИНН",
	msgConsoleRegistrationsColEmail:   "Email компании",
	msgConsoleRegistrationsColWebsite: "Сайт",
	msgConsoleRegistrationsColLegal:   "Юридические данные",
	msgConsoleRegistrationsColCreated: "Подана",
	msgConsoleRegistrationsApprove:    "Подтвердить",
	msgConsoleRegistrationsReject:     "Отклонить",
	msgConsoleRegistrationsReason:     "Комментарий",
	msgConsoleProfilesTitle:           "Профили на согласовании",
	msgConsoleProfilesEmpty:           "Профилей на согласовании нет.",
	msgConsoleProfilesUnavailable:     "Содержимое профиля недоступно — решение не принимается.",
	msgConsoleProfilesDescription:     "Описание компании",
	msgConsoleProfilesTarget:          "Кого ищет",
	msgConsoleProfilesServices:        "Услуги",
	msgConsoleProfilesApprove:         "Подтвердить профиль",
	msgConsoleProfilesReject:          "Отклонить профиль",

	msgTopologyTitle:      "Топология сервисов",
	msgTopologyGenerated:  "Построено",
	msgTopologyUnavail:    "Модель топологии недоступна",
	msgTopologyWarnings:   "Предупреждения",
	msgTopologyGraphAria:  "Граф сервисной топологии",
	msgTopologyNodesHead:  "Узлы",
	msgTopologyEdgesHead:  "Связи",
	msgTopologyEmpty:      "Узлы не объявлены.",
	msgTopologyColService: "Сервис",
	msgTopologyColKind:    "Вид",
	msgTopologyColZone:    "Зона",
	msgTopologyColRoles:   "Роли",
	msgTopologyColState:   "Состояние",
	msgTopologyColConns:   "Связи",
	msgTopologyColContr:   "Контракты",
	msgTopologyColEdge:    "Связь",
	msgTopologyColFrom:    "От",
	msgTopologyColTo:      "К",
	msgTopologyColIntegr:  "Интеграция",
	msgTopologyColTransp:  "Транспорт",
	msgTopologyColMech:    "Механизм",
	msgTopologyColContrct: "Контракт",

	msgTopologyNodeTitle:    "Карточка узла",
	msgTopologyNodeMissing:  "Узел не найден",
	msgTopologyNodeID:       "Идентификатор",
	msgTopologyNodeKind:     "Вид",
	msgTopologyNodeZone:     "Зона данных",
	msgTopologyNodeRoles:    "Роли запуска",
	msgTopologyNodeGroups:   "Группы deploy",
	msgTopologyNodeAddr:     "Адреса",
	msgTopologyNodeState:    "Состояние",
	msgTopologyNodeContract: "Контракты",
	msgTopologyNodeConns:    "Связи",
	msgTopologyNodeBack:     "К топологии",
	msgTopologyNodeEmpty:    "нет",
	msgTopologyNodeLoaded:   "Карточка узла обновлена",

	msgTopologyKindService: "сервис",
	msgTopologyKindInfra:   "инфраструктура",
	msgTopologyKindUnknown: "неизвестно",
	msgTopologyZoneGreen:   "зелёная",
	msgTopologyZoneRed:     "красная",
	msgTopologyZoneMixed:   "смешанная",
	msgTopologyZoneInfra:   "инфраструктурная",

	msgTopologyStateUp:       "работает",
	msgTopologyStateDegraded: "деградация",
	msgTopologyStateDown:     "недоступен",
	msgTopologyStateUnknown:  "неизвестно",
	msgTopologyStateIconUp:   "✓",
	msgTopologyStateIconDeg:  "!",
	msgTopologyStateIconDown: "✕",
	msgTopologyStateIconUnk:  "?",

	msgTopologyIntensityUnknown: "нет данных",
	msgTopologyIntensityNormal:  "обычная",
	msgTopologyIntensityHot:     "горячая",

	msgTopologyPanelReqRate: "Частота запросов",
	msgTopologyPanelErrRate: "Доля ошибок",

	msgTopologyMetricsUnavail: "Метрики недоступны",
	msgTopologyMetricsNoData:  "нет данных",

	msgCockpitNavOperations:               "Операции",
	msgCockpitTitleOperations:             "Операции: цифры процесса",
	msgCockpitTitleAgents:                 "Живая лента агентов",
	msgCockpitTitleAgent:                  "Подробности агента",
	msgCockpitTitleProcess:                "Процессы Temporal",
	msgCockpitTitleDashboard:              "Дашборд по клиентам",
	msgCockpitRequestsHeading:             "Заявки и исполнение",
	msgCockpitRequestsUnavailable:         "Список заявок недоступен",
	msgCockpitRequestsEmpty:               "Заявок пока нет",
	msgCockpitRequestColID:                "Заявка",
	msgCockpitRequestColStatus:            "Статус заявки",
	msgCockpitRequestColPhase:             "Фаза исполнения",
	msgCockpitRequestColWorkflow:          "Workflow / Run",
	msgCockpitRequestColCounts:            "Найдено / цель",
	msgCockpitRequestExecutionUnavailable: "Исполнение недоступно",
	msgCockpitRequestCreated:              "Создана",
	msgCockpitRequestProcessing:           "В работе",
	msgCockpitRequestCancelled:            "Отменена",
	msgCockpitRequestFulfilled:            "Выполнена",
	msgCockpitRequestRejected:             "Отклонена",
	msgCockpitRequestNeedsRevision:        "Требует доработки",
	msgCockpitRequestFailed:               "Сбой",

	msgCockpitColCategory:            "Категория",
	msgCockpitColClient:              "Клиент",
	msgCockpitColTotal:               "Всего",
	msgCockpitColDate:                "Дата",
	msgCockpitNumbersEmpty:           "Данных процесса пока нет.",
	msgCockpitNumbersUnavailable:     "Данные процесса недоступны",
	msgCockpitNumbersPeriods:         "Разбивка по времени",
	msgCockpitCategoryFound:          "Найдено",
	msgCockpitCategoryProcessed:      "Обработано",
	msgCockpitCategoryRejected:       "Отклонено",
	msgCockpitCategoryInProgress:     "В работе",
	msgCockpitCategoryResult:         "Результат",
	msgCockpitCategoryIconFound:      "◈",
	msgCockpitCategoryIconProcessed:  "■",
	msgCockpitCategoryIconRejected:   "✕",
	msgCockpitCategoryIconInProgress: "▸",
	msgCockpitCategoryIconResult:     "✓",

	msgCockpitStateRunning:              "выполняется",
	msgCockpitStateCompleted:            "завершён",
	msgCockpitStateFailed:               "отказ",
	msgCockpitStateQueued:               "в очереди",
	msgCockpitStateWaiting:              "ожидание",
	msgCockpitStateUnknown:              "неизвестно",
	msgCockpitStateAwaitingApproval:     "ожидает утверждения",
	msgCockpitStateBlocked:              "заблокирован",
	msgCockpitStateNeedsAttention:       "требует вмешательства",
	msgCockpitStateStopped:              "остановлен",
	msgCockpitStateIconRunning:          "▸",
	msgCockpitStateIconCompleted:        "✓",
	msgCockpitStateIconFailed:           "✕",
	msgCockpitStateIconQueued:           "…",
	msgCockpitStateIconUnknown:          "?",
	msgCockpitStateIconAwaitingApproval: "⌛",
	msgCockpitStateIconStopped:          "■",
	msgCockpitProcessCanceled:           "отменён",
	msgCockpitProcessTerminated:         "прерван",
	msgCockpitProcessContinued:          "продолжен",
	msgCockpitProcessTimedOut:           "таймаут",

	msgCockpitKindActivity:       "активность",
	msgCockpitKindTimer:          "таймер",
	msgCockpitKindSignal:         "сигнал",
	msgCockpitOutcomeScheduled:   "запланирован",
	msgCockpitOutcomeCompleted:   "успех",
	msgCockpitOutcomeFailed:      "отказ",
	msgCockpitOutcomeTimedOut:    "таймаут",
	msgCockpitOutcomeCanceled:    "отменён",
	msgCockpitOutcomeUnavailable: "недоступно",
	msgCockpitStateWaitStarted:   "ожидание начато",
	msgCockpitStateWaitFired:     "таймер сработал",
	msgCockpitStateWaitReceived:  "сигнал получен",

	msgCockpitColRun:            "Запуск",
	msgCockpitColCompany:        "Компания",
	msgCockpitColState:          "Состояние",
	msgCockpitColStep:           "Текущий шаг",
	msgCockpitColUpdated:        "Обновлено",
	msgCockpitAgentsEmpty:       "Агентов пока нет.",
	msgCockpitAgentsUnavailable: "Лента агентов недоступна",
	msgCockpitAgentsAria:        "Живая лента агентов",
	msgCockpitAgentsLive:        "Лента агентов обновлена",
	msgCockpitAgentLink:         "Подробнее",
	msgCockpitAgentsExpand:      "Подробности строки",

	msgCockpitAgentUnavailable: "Подробности недоступны",
	msgCockpitAgentMissing:     "Агент не найден",
	msgCockpitAgentEvents:      "События",
	msgCockpitAgentReasons:     "Причины отказов",
	msgCockpitAgentJobs:        "Связанные задания",
	msgCockpitAgentNoEvents:    "Событий нет.",
	msgCockpitAgentNoReasons:   "Отказов нет.",
	msgCockpitAgentNoJobs:      "Связанных заданий нет.",
	msgCockpitAgentBack:        "К ленте",
	msgCockpitColTime:          "Время",
	msgCockpitColOutcome:       "Исход",
	msgCockpitColReason:        "Причина",
	msgCockpitColCount:         "Число",
	msgCockpitColJob:           "Задание",
	msgCockpitColKind:          "Вид",

	msgCockpitProcessUnavailable:  "Процессы Temporal недоступны",
	msgCockpitProcessEmpty:        "Исполнений не найдено.",
	msgCockpitProcessAria:         "Граф процессов Temporal",
	msgCockpitProcessLive:         "Граф процессов обновлён",
	msgCockpitProcessTimeline:     "Ход выбранного исполнения",
	msgCockpitProcessTimelineAria: "Цепочка шагов и ожиданий",
	msgCockpitProcessSelect:       "Показать ход",
	msgCockpitColWorkflow:         "Исполнение",
	msgCockpitColType:             "Тип",
	msgCockpitColStarted:          "Начато",

	msgCockpitDashboardUnavailable: "Агрегаты недоступны",
	msgCockpitDashboardThreshold:   "Порог минимальной группы",
	msgCockpitDashboardEmpty:       "Срезов выше порога нет.",
	msgCockpitColAnomalies:         "Аномалии",

	msgCockpitPanelsHeading:        "Панели",
	msgCockpitPanelSource:          "Источник",
	msgCockpitPanelRefresh:         "Обновление",
	msgCockpitPanelUnavailable:     "недоступно",
	msgCockpitPanelNoData:          "нет данных",
	msgCockpitPanelHidden:          "панель скрыта",
	msgCockpitPanelHide:            "Скрыть",
	msgCockpitPanelShow:            "Показать",
	msgCockpitSourceMetrics:        "метрики",
	msgCockpitSourceProcess:        "процессы",
	msgCockpitSourceAggregate:      "агрегаты",
	msgCockpitRefreshPoll:          "опрос",
	msgCockpitRefreshStatic:        "при загрузке",
	msgCockpitPanelProcess:         "Сводка процессов",
	msgCockpitPanelWorkflowStarted: "Старт процессов",
	msgCockpitPanelWorkflowErrors:  "Ошибки старта процессов",
	msgCockpitPanelHTTPErrorRatio:  "Доля серверных ошибок",

	msgCockpitReasonNone:                "нет",
	msgCockpitReasonNotRelevant:         "не подходит",
	msgCockpitReasonNoContact:           "нет контакта",
	msgCockpitReasonExtractionError:     "ошибка извлечения",
	msgCockpitReasonTimeout:             "таймаут",
	msgCockpitReasonProviderUnavailable: "провайдер недоступен",

	msgCockpitAnomalyRejectionSpike: "всплеск отказов",
	msgCockpitAnomalyStalled:        "процесс замер",
	msgCockpitAnomalyUnknown:        "аномалия",

	msgCockpitNoticeBadRequest:        "Некорректный запрос",
	msgCockpitNoticeCSRF:              "Отсутствует или неверен токен CSRF",
	msgCockpitNoticeBadPanel:          "Неизвестная панель",
	msgCockpitNoticePanelsUnavailable: "Настройки панелей недоступны",

	// Читаемые подписи статусов заявки в портале клиента (change
	// console-liquid-glass): шаблон берёт их функцией label.
	"portal.request.status.submitted":      "Принята",
	"portal.request.status.validating":     "На проверке",
	"portal.request.status.validated":      "Проверена",
	"portal.request.status.processing":     "В работе",
	"portal.request.status.fulfilled":      "Выполнена",
	"portal.request.status.rejected":       "Отклонена",
	"portal.request.status.cancelled":      "Отменена",
	"portal.request.status.needs-revision": "На доработке",
	"portal.request.status.failed":         "Сбой",
	"portal.request.failure":               "Причина сбоя:",
}

// consoleMessages возвращает каталог контекста поверх общего каталога.
func consoleMessages() *components.Messages {
	return components.WithMessages(components.DefaultMessages(), consoleCatalog)
}
