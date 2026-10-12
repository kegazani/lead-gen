// Публичная поверхность корневого пакета для composition root: реестр
// объявленных блоков агента и первый набор блоков (design.md, Реш. 3, 16).
//
// Реестр блоков — данные, а не код супервизора: состав пайплайна расширяется
// регистрацией блока и публикацией новой версии, супервизор при этом не меняется.
// Незарегистрированный блок не исполняется и не подменяется (спека
// company-agent-orchestration); блок без подключённого исполнителя честно
// недоступен и не выдаётся за успех (AGENTS.md §3).
package agentorchestration

import (
	"fmt"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// Публичные псевдонимы типов контекста, чтобы composition root мог собирать
// реестр блоков и регистрировать исполнители, не импортируя internal-пакеты.
type (
	// Block — объявление блока обработки.
	Block = domain.Block
	// BlockRegistry — реестр объявленных блоков.
	BlockRegistry = domain.BlockRegistry
	// BlockRef — ссылка шага пайплайна на зарегистрированный блок.
	BlockRef = domain.BlockRef
	// BlockExecutor — исполнитель блока по контракту владельца.
	BlockExecutor = ports.BlockExecutor
	// BlockOutcome — исход выполнения шага.
	BlockOutcome = domain.BlockOutcome
)

// Публичные псевдонимы инъектируемых портов блоков, чьи контракты владельцев ещё
// не опубликованы или подключаются composition root'ом: оценка релевантности,
// подача черновика на утверждение и источник утверждённого действия.
type (
	// RelevanceEvaluator — порт read-контракта lead-relevance.
	RelevanceEvaluator = ports.RelevanceEvaluator
	// RelevanceQuery — вход оценки релевантности.
	RelevanceQuery = ports.RelevanceQuery
	// RelevanceEvaluation — исход оценки релевантности.
	RelevanceEvaluation = ports.RelevanceEvaluation
	// RelevanceVerdict — вердикт оценки релевантности.
	RelevanceVerdict = ports.RelevanceVerdict
	// ApprovalSubmitter — порт подачи черновика на утверждение.
	ApprovalSubmitter = ports.ApprovalSubmitter
	// ApprovalSubmission — черновик, подаваемый на утверждение.
	ApprovalSubmission = ports.ApprovalSubmission
	// ApprovedActionSource — источник утверждённого действия для шага отправки.
	ApprovedActionSource = ports.ApprovedActionSource
	// ApprovedActionLookup — вход запроса утверждённого действия.
	ApprovedActionLookup = ports.ApprovedActionLookup
)

// Вердикты оценки релевантности, публикуемые для composition root.
const (
	// RelevanceSuitable — компания подходит в контексте задания.
	RelevanceSuitable = ports.RelevanceSuitable
	// RelevanceUnsuitable — компания не подходит в контексте задания.
	RelevanceUnsuitable = ports.RelevanceUnsuitable
)

// Исходы инъектируемых портов блоков, публикуемые для composition root и тестов.
var (
	// ErrRelevanceRejected — оценка отклонена владельцем.
	ErrRelevanceRejected = ports.ErrRelevanceRejected
	// ErrRelevanceUnavailable — владелец оценки недоступен.
	ErrRelevanceUnavailable = ports.ErrRelevanceUnavailable
	// ErrApprovalSubmitRejected — подача на утверждение отклонена владельцем.
	ErrApprovalSubmitRejected = ports.ErrApprovalSubmitRejected
	// ErrApprovalSubmitUnavailable — владелец утверждения недоступен.
	ErrApprovalSubmitUnavailable = ports.ErrApprovalSubmitUnavailable
	// ErrApprovedActionPending — решение по черновику ещё не принято.
	ErrApprovedActionPending = ports.ErrApprovedActionPending
	// ErrApprovedActionRejected — оператор отклонил черновик.
	ErrApprovedActionRejected = ports.ErrApprovedActionRejected
	// ErrApprovedActionUnavailable — источник решения недоступен.
	ErrApprovedActionUnavailable = ports.ErrApprovedActionUnavailable
)

// Ссылки блоков первого набора (design.md, Реш. 3). Идентификатор блока совпадает
// с именем в описании пайплайна.
const (
	BlockSearch        BlockRef = "search"
	BlockClassify      BlockRef = "classify"
	BlockEnrich        BlockRef = "enrich"
	BlockVerifyEmail   BlockRef = "verify-email"
	BlockSizeAssess    BlockRef = "size-assess"
	BlockGenerateEmail BlockRef = "generate-email"
	BlockAwaitApproval BlockRef = "await-approval"
	BlockSend          BlockRef = "send"
	BlockObserve       BlockRef = "observe"
	BlockDecideNext    BlockRef = "decide-next"
)

// blockSpec — описание объявленного блока первого набора: ссылка, имя, контракт
// владельца, признаки необратимости/обязательности и признак реализованности.
type blockSpec struct {
	ref          BlockRef
	name         string
	contract     string
	irreversible bool
	mandatory    bool
	// implemented — контракт-владелец опубликован и у блока есть исполнитель в
	// контексте. Только реализованные блоки входят в первый публикуемый пайплайн:
	// обязательный блок без исполнителя заведомо заблокировал бы любой запуск
	// (спека company-agent-orchestration, «Честная недоступность блока»).
	implemented bool
}

// defaultBlockSpecs — первый набор блоков пайплайна агента (design.md, Реш. 3).
//
// Контракт — имя опубликованного контракта владельца, через который блок
// исполняется (Реш. 6). Блоки, чьи контракты ещё не подключены (релевантность,
// верификация адреса, размер компании, материал/черновик, доставка), объявлены,
// но исполнителя не имеют: их шаг честно недоступен (спека, «Честная
// недоступность блока»). Блок решения «decide-next» — внутренний: внешнего
// контракта у него нет.
func defaultBlockSpecs() []blockSpec {
	return []blockSpec{
		{ref: BlockSearch, name: "Поиск компании", contract: "lead-search", mandatory: true, implemented: true},
		{ref: BlockClassify, name: "Оценка релевантности", contract: "lead-relevance", mandatory: true, implemented: true},
		{ref: BlockEnrich, name: "Обогащение компании", contract: "lead-profile"},
		{ref: BlockVerifyEmail, name: "Верификация адреса", contract: "lead-verified", mandatory: true},
		{ref: BlockSizeAssess, name: "Оценка размера компании", contract: "company-profile"},
		{ref: BlockGenerateEmail, name: "Черновик письма", contract: "content-request", mandatory: true, implemented: true},
		{ref: BlockAwaitApproval, name: "Ожидание утверждения", contract: "campaign-command", implemented: true},
		{ref: BlockSend, name: "Отправка письма", contract: "delivery-command", irreversible: true, mandatory: true, implemented: true},
		{ref: BlockObserve, name: "Наблюдение реакции", contract: "engagement-event"},
		{ref: BlockDecideNext, name: "Решение о следующем шаге"},
	}
}

// NewBlockRegistry собирает реестр объявленных блоков из описаний. Дублирующаяся
// ссылка — ошибка: две трактовки одного блока недопустимы. Composition root
// использует конструктор для расширения состава блоков без правки супервизора.
func NewBlockRegistry(blocks ...Block) (*BlockRegistry, error) {
	return domain.NewBlockRegistry(blocks...)
}

// NewBlock собирает объявление блока для расширения реестра.
func NewBlock(ref BlockRef, name, contract string, irreversible, mandatory bool) (Block, error) {
	return domain.NewBlock(ref, name, contract, irreversible, mandatory)
}

// DefaultBlocks возвращает реестр объявленных блоков первого набора. Публикация
// версии пайплайна, ссылающейся на эти блоки, становится доступной: состав шагов
// проверяется против этого реестра.
func DefaultBlocks() (*BlockRegistry, error) {
	specs := defaultBlockSpecs()
	blocks := make([]Block, 0, len(specs))
	for _, spec := range specs {
		block, err := domain.NewBlock(spec.ref, spec.name, spec.contract, spec.irreversible, spec.mandatory)
		if err != nil {
			return nil, fmt.Errorf("agent-orchestration: объявление блока %q: %w", spec.ref, err)
		}
		blocks = append(blocks, block)
	}
	registry, err := domain.NewBlockRegistry(blocks...)
	if err != nil {
		return nil, fmt.Errorf("agent-orchestration: реестр блоков: %w", err)
	}
	return registry, nil
}

// DefaultPipelineSteps возвращает первый набор шагов пайплайна — реализованные
// блоки в порядке исполнения. Состав шагов — данные: публикация версии создаёт
// неизменяемую версию, а новый блок добавляется её пересмотром (design.md,
// Реш. 3). Объявленные, но ещё не реализованные блоки (enrich, verify-email,
// size-assess, observe, decide-next) остаются в реестре (DefaultBlocks), но в
// первый пайплайн не входят: обязательный блок без исполнителя заблокировал бы
// каждый запуск. Они добавляются новой версией пайплайна после публикации их
// контрактов-владельцев.
func DefaultPipelineSteps() []PipelineStep {
	specs := defaultBlockSpecs()
	steps := make([]PipelineStep, 0, len(specs))
	for _, spec := range specs {
		if !spec.implemented {
			continue
		}
		steps = append(steps, PipelineStep{ID: string(spec.ref), BlockRef: string(spec.ref)})
	}
	return steps
}
