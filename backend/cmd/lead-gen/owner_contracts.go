// Проводка владельческих контрактов к блокам agent-orchestration и к контуру
// обратной связи analytics (change intelligent-lead-operations, задача 7.3;
// change search-quality-feedback).
//
// Точка сборки связывает блоки агента и аналитику с владельцами по объявленным
// контрактам: при co-location — ленивым прямым адаптером владельца, при
// разделении сервисов — сетевым клиентом того же контракта (выбор по топологии,
// как у ленты и агрегатов экрана операций). Порты, чьи контракты владельцем не
// опубликованы, остаются незаданными: соответствующий блок честно недоступен, а
// не подменяется выдуманным результатом (AGENTS.md §3).
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	contentproduction "lead-gen/backend/contexts/content-production"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/content-production/contentrequest"
	contentrequesthttpport "lead-gen/contracts/content-production/contentrequest/httpport"
	"lead-gen/contracts/lead-management/leadrelevance"
	leadrelevancehttpport "lead-gen/contracts/lead-management/leadrelevance/httpport"
	"lead-gen/contracts/lead-management/recommendationapply"
	recommendationapplyhttpport "lead-gen/contracts/lead-management/recommendationapply/httpport"
	approvedactionhttpport "lead-gen/contracts/outreach-orchestration/approvedaction/httpport"
)

// leadManagementStore — адаптеры хранилища lead-management, собранные сервисом
// владельца. Co-location-контракты (lead-relevance, recommendation-apply) читают
// то же хранилище, что и сам сервис: второго экземпляра (особенно второй
// in-memory базы, из-за которой задание поиска ушло бы «в никуда») не заводится.
// Стор заполняется при сборке сервиса владельца, поэтому до её завершения пуст.
type leadManagementStore struct {
	mu    sync.Mutex
	repos leadmanagement.Repositories
	ready bool
}

// newLeadManagementStore создаёт пустой стор. Точка входа задаёт его до сборки
// любого сервиса: мост approved-action собирает lead-management раньше прочих.
func newLeadManagementStore() *leadManagementStore { return &leadManagementStore{} }

// put сохраняет адаптеры владельца. Вызывается точкой сборки из
// buildLeadManagement — один раз на процесс.
func (s *leadManagementStore) put(repos leadmanagement.Repositories) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.repos = repos
	s.ready = true
	s.mu.Unlock()
}

// load возвращает сохранённые адаптеры. Второе значение ложно, если владелец ещё
// не собран: co-location-контракт не выдумывает хранилище.
func (s *leadManagementStore) load() (leadmanagement.Repositories, bool) {
	if s == nil {
		return leadmanagement.Repositories{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repos, s.ready
}

// relevanceEvaluator связывает read-контракт lead-relevance (владелец —
// lead-management) с портом блока classify (agent-orchestration): переводит запрос
// (задание, компания) и вердикт.
//
// Вердикт «подходит» — единственный, дающий основание инициировать письмо; всё
// остальное блокирует обработку компании. «Гипотеза» (профиль совпал, сигнала
// потребности нет) — по определению владельца не «подходит» и в консоли стоит во
// второй группе вместе с «не подходит»; различие «нет сигнала» и «опровергнуто»
// сохраняет read-модель `lead-quality-insights`, а не бинарный вердикт агента.
// Неизвестный вердикт не выдаётся за вердикт: владелец для блока недоступен.
type relevanceEvaluator struct {
	port leadrelevance.Port
}

// newRelevanceEvaluator собирает мост поверх опубликованного контракта владельца.
func newRelevanceEvaluator(port leadrelevance.Port) *relevanceEvaluator {
	return &relevanceEvaluator{port: port}
}

// Проверка соответствия порту блока classify на этапе компиляции.
var _ agentorchestration.RelevanceEvaluator = (*relevanceEvaluator)(nil)

// Evaluate реализует agentorchestration.RelevanceEvaluator.
func (a *relevanceEvaluator) Evaluate(ctx context.Context, query agentorchestration.RelevanceQuery) (agentorchestration.RelevanceEvaluation, error) {
	if a == nil || a.port == nil {
		return agentorchestration.RelevanceEvaluation{}, agentorchestration.ErrRelevanceUnavailable
	}
	assessment, err := a.port.Read(ctx, leadrelevance.Request{
		JobID:     query.MissionID,
		CompanyID: query.CompanyID,
	})
	if err != nil {
		switch {
		case errors.Is(err, leadrelevance.ErrRejected):
			return agentorchestration.RelevanceEvaluation{}, fmt.Errorf("%w: %v", agentorchestration.ErrRelevanceRejected, err)
		default:
			// ErrNotFound (оценки нет) и ErrUnavailable (владелец недоступен) —
			// единый исход недоступности вердикта: блок не выдумывает оценку.
			return agentorchestration.RelevanceEvaluation{}, fmt.Errorf("%w: %v", agentorchestration.ErrRelevanceUnavailable, err)
		}
	}
	verdict, ok := agentVerdict(assessment.Verdict)
	if !ok {
		return agentorchestration.RelevanceEvaluation{}, fmt.Errorf("%w: вердикт оценки %q не распознан", agentorchestration.ErrRelevanceUnavailable, assessment.Verdict)
	}
	return agentorchestration.RelevanceEvaluation{
		Verdict:  verdict,
		BasisRef: assessmentBasisRef(assessment),
	}, nil
}

// agentVerdict переводит вердикт read-контракта в закрытый набор вердиктов блока.
// «Подходит» — единственный подтверждающий вердикт; «гипотеза» и «не подходит»
// блокируют обработку, не давая основания инициировать письмо.
func agentVerdict(verdict string) (agentorchestration.RelevanceVerdict, bool) {
	switch verdict {
	case leadrelevance.VerdictFits:
		return agentorchestration.RelevanceSuitable, true
	case leadrelevance.VerdictHypothesis, leadrelevance.VerdictUnsuitable:
		return agentorchestration.RelevanceUnsuitable, true
	default:
		return "", false
	}
}

// assessmentBasisRef — непрозрачная ссылка на оценку-основание (пара задание,
// компания). Контактных данных и содержимого красной зоны здесь нет.
func assessmentBasisRef(assessment leadrelevance.Assessment) string {
	if strings.TrimSpace(assessment.JobID) == "" || strings.TrimSpace(assessment.CompanyID) == "" {
		return ""
	}
	return assessment.JobID + "/" + assessment.CompanyID
}

// leadRelevanceCoLocated — ленивый порт блока classify поверх co-located
// lead-management. Владелец собирается точкой входа, поэтому контракт берётся при
// первом обращении, а не в момент сборки агента: иначе сборка агента потянула бы
// сборку владельца и замкнула бы порядок инициализации.
type leadRelevanceCoLocated struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService
	store     *leadManagementStore

	once sync.Once
	port agentorchestration.RelevanceEvaluator
	err  error
}

// resolve собирает контракт владельца по требованию.
func (p *leadRelevanceCoLocated) resolve() (agentorchestration.RelevanceEvaluator, error) {
	p.once.Do(func() {
		if _, err := p.buildOnce(p.sel); err != nil {
			p.err = fmt.Errorf("lead-management: сборка владельца оценки: %w", err)
			return
		}
		repos, ok := p.store.load()
		if !ok {
			p.err = errors.New("lead-management: хранилище оценок не собрано")
			return
		}
		p.port = newRelevanceEvaluator(leadmanagement.NewLeadRelevance(leadmanagement.LeadRelevanceDeps{Repositories: repos}))
	})
	return p.port, p.err
}

// Evaluate реализует agentorchestration.RelevanceEvaluator.
func (p *leadRelevanceCoLocated) Evaluate(ctx context.Context, query agentorchestration.RelevanceQuery) (agentorchestration.RelevanceEvaluation, error) {
	port, err := p.resolve()
	if err != nil {
		return agentorchestration.RelevanceEvaluation{}, err
	}
	return port.Evaluate(ctx, query)
}

// recommendationApplyCoLocated — ленивый порт контракта recommendation-apply поверх
// co-located lead-management: применение рекомендации анализа как нового запуска
// поиска. Резолвится при первом обращении (см. leadRelevanceCoLocated).
type recommendationApplyCoLocated struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService
	store     *leadManagementStore

	once sync.Once
	port recommendationapply.Port
	err  error
}

// resolve собирает контракт владельца по требованию.
func (p *recommendationApplyCoLocated) resolve() (recommendationapply.Port, error) {
	p.once.Do(func() {
		if _, err := p.buildOnce(p.sel); err != nil {
			p.err = fmt.Errorf("lead-management: сборка владельца поиска: %w", err)
			return
		}
		repos, ok := p.store.load()
		if !ok {
			p.err = errors.New("lead-management: хранилище заданий не собрано")
			return
		}
		p.port = leadmanagement.NewRecommendationApply(leadmanagement.RecommendationApplyDeps{Repositories: repos})
	})
	return p.port, p.err
}

// ApplyRecommendation реализует recommendationapply.Port.
func (p *recommendationApplyCoLocated) ApplyRecommendation(ctx context.Context, application recommendationapply.Application) (recommendationapply.Receipt, error) {
	port, err := p.resolve()
	if err != nil {
		return recommendationapply.Receipt{}, fmt.Errorf("%w: %v", recommendationapply.ErrUnavailable, err)
	}
	return port.ApplyRecommendation(ctx, application)
}

// contentRequestCoLocated — ленивый порт контракта content-request поверх
// co-located content-production: производство материала письма блоком агента.
// Владелец отдаёт срез сборки, поверх которого контракт собирается при первом
// обращении.
type contentRequestCoLocated struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService

	once sync.Once
	port contentrequest.Port
	err  error
}

// resolve собирает контракт владельца по требованию.
func (p *contentRequestCoLocated) resolve() (contentrequest.Port, error) {
	p.once.Do(func() {
		service, err := p.buildOnce(p.sel)
		if err != nil {
			p.err = fmt.Errorf("content-production: сборка владельца материала: %w", err)
			return
		}
		owner, ok := service.(*contentproduction.Service)
		if !ok {
			p.err = fmt.Errorf("content-production: владелец собран не тем типом сервиса: %T", service)
			return
		}
		p.port = contentproduction.NewContentRequest(owner.Slice())
	})
	return p.port, p.err
}

// RequestContent реализует contentrequest.Port.
func (p *contentRequestCoLocated) RequestContent(ctx context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
	port, err := p.resolve()
	if err != nil {
		return contentrequest.Result{}, fmt.Errorf("%w: %v", contentrequest.ErrUnavailable, err)
	}
	return port.RequestContent(ctx, in)
}

// assembleRecommendationApply связывает контракт recommendation-apply владельца
// lead-management с контуром обратной связи analytics (Config.RecommendationApply):
// задан адрес владельца — сетевой клиент контракта; иначе владелец выбран —
// ленивый co-located порт. Иначе контур не подключён, и применение рекомендации
// честно недоступно (AGENTS.md §3).
func assembleRecommendationApply(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	if addr := strings.TrimSpace(opts.recommendationApplyURL); addr != "" {
		client, err := recommendationapplyhttpport.New(addr)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент recommendation-apply", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.recommendationApply = client
		logger.Info("lead-gen: контур обратной связи анализа подключён по сети", observ.Fields{}, "addr", addr)
		return
	}
	sel, ok := findSelected(selected, leadmanagement.ServiceID)
	if !ok {
		logger.Warn("lead-gen: владелец lead-management не выбран — применение рекомендации анализа честно недоступно", observ.Fields{})
		return
	}
	opts.recommendationApply = &recommendationApplyCoLocated{buildOnce: buildOnce, sel: sel, store: opts.leadStore}
	logger.Info("lead-gen: контур обратной связи анализа читает владельца lead-management (co-location)", observ.Fields{})
}

// assembleAgentOwnerPorts связывает порты владельцев с блоками агента
// (agent-orchestration): оценка релевантности, производство материала, а также
// подача черновика на утверждение и чтение утверждённого действия. Каждый порт
// берётся по топологии (co-location либо сеть); незаданный порт оставляет блок
// честно недоступным, а не подменяется выдуманным результатом.
func assembleAgentOwnerPorts(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	assembleLeadRelevance(selected, opts, buildOnce, logger)
	assembleAgentContentRequest(selected, opts, buildOnce, logger)
	assembleAgentApprovedAction(opts, logger)
	assembleAgentApprovalContracts(selected, opts, buildOnce, logger)
}

// assembleLeadRelevance связывает блок classify с read-контрактом lead-relevance.
func assembleLeadRelevance(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	if addr := strings.TrimSpace(opts.leadRelevanceURL); addr != "" {
		client, err := leadrelevancehttpport.New(addr)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент lead-relevance", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.leadRelevance = newRelevanceEvaluator(client)
		logger.Info("lead-gen: блок classify читает контракт lead-relevance по сети", observ.Fields{}, "addr", addr)
		return
	}
	sel, ok := findSelected(selected, leadmanagement.ServiceID)
	if !ok {
		logger.Warn("lead-gen: владелец lead-management не выбран — блок classify честно недоступен", observ.Fields{})
		return
	}
	opts.leadRelevance = &leadRelevanceCoLocated{buildOnce: buildOnce, sel: sel, store: opts.leadStore}
	logger.Info("lead-gen: блок classify читает контракт lead-relevance от владельца (co-location)", observ.Fields{})
}

// assembleAgentContentRequest связывает блок производства черновика с контрактом
// content-request. Сетевой путь — при заданном адресе; иначе co-located
// content-production; иначе блок честно недоступен.
func assembleAgentContentRequest(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	if addr := strings.TrimSpace(opts.contentRequestURL); addr != "" {
		client, err := contentrequesthttpport.New(addr)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент content-request", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.agentContentRequest = client
		logger.Info("lead-gen: блок производства материала читает контракт content-request по сети", observ.Fields{}, "addr", addr)
		return
	}
	sel, ok := findSelected(selected, contentproduction.ServiceID)
	if !ok {
		logger.Warn("lead-gen: владелец content-production не выбран — блок производства материала честно недоступен", observ.Fields{})
		return
	}
	opts.agentContentRequest = &contentRequestCoLocated{buildOnce: buildOnce, sel: sel}
	logger.Info("lead-gen: блок производства материала читает контракт content-request от владельца (co-location)", observ.Fields{})
}

// assembleAgentApprovedAction связывает издателя контракта approved-action для
// блока отправки: сетевой клиент при заданном адресе приёма message-delivery либо
// in-proc мост (co-location). Источник решения (read-контракт draftdecision)
// связывается отдельно в assembleAgentApprovalContracts; отсутствие издателя
// оставляет блок отправки честно недоступным.
func assembleAgentApprovedAction(opts *buildOptions, logger observ.Logger) {
	switch {
	case strings.TrimSpace(opts.approvedActionURL) != "":
		client, err := approvedactionhttpport.New(opts.approvedActionURL)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент approved-action для агента", err, observ.Fields{}, "addr", opts.approvedActionURL)
			return
		}
		opts.approvedAction = client
	case opts.approvedActionSink != nil:
		opts.approvedAction = opts.approvedActionSink
	default:
		logger.Warn("lead-gen: издатель approved-action для агента не подключён — блок отправки честно недоступен", observ.Fields{})
	}
}
