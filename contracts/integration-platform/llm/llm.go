// Package llm — порт LLM. Порт несёт операции:
//
//   - Plan — разложение готового запроса оператора в серию поисковых запросов и
//     стартовых URL («куда идти в интернете»);
//   - Suggest — подбор секторов, ниш и seed-запросов по описанию задачи
//     оператора («что искать»);
//   - Converse — один ход диалога: уточняющий вопрос агента и черновик целей по
//     истории разговора («договориться, что искать»);
//   - Interpret — превращение бизнес-запроса в критерии релевантности,
//     отдельные от технического плана обхода («почему компания решает задачу»);
//   - Assess — оценка компании относительно критериев задания: вердикт, тип
//     сигнала и свидетельства со ссылками на источники.
//
// Экстракция контактов вынесена в порт crawl (structured-режим Firecrawl), а не
// сюда; этот порт отвечает только за «рассуждение, куда идти в интернете».
// Реализация обязана уважать context.Context, допускать повторные вызовы и не
// протекать ошибками SDK наружу.
package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
)

// ErrInvalidPlan отличает отвергнутую схему ответа от недоступности модели.
var ErrInvalidPlan = errors.New("llm: недопустимая схема плана")

// Port — порт LLM. Реализуется OpenAI-совместимым клиентом (провайдер DeepSeek).
type Port interface {
	// Plan раскладывает запрос оператора в серию поисковых запросов и
	// стартовых URL, возвращая расход токенов вызова.
	Plan(ctx context.Context, request PlanRequest) ([]Target, Usage, error)

	// Suggest по описанию бизнеса/задачи оператора подбирает сектора, ниши и
	// готовые seed-запросы, возвращая расход токенов вызова.
	Suggest(ctx context.Context, request SuggestRequest) ([]Suggestion, Usage, error)

	// Converse выполняет один ход диалога: принимает историю разговора и
	// возвращает реплику агента, черновик целей и признак готовности плана,
	// вместе с расходом токенов вызова.
	Converse(ctx context.Context, request ConverseRequest) (ConverseReply, Usage, error)

	// ConverseStream выполняет один ход диалога с потоковой отдачей реплики:
	// фрагменты текста приходят по мере генерации, а завершающий кадр несёт
	// черновик целей, лимиты и признак готовности. Ошибка доходит до потребителя
	// последним элементом последовательности; отмена — через ctx.
	//
	// Последовательность ленива: потребитель, прекративший обход, останавливает
	// генерацию без утечки производителя.
	ConverseStream(ctx context.Context, request ConverseRequest) iter.Seq2[ConverseDelta, error]

	// Interpret превращает бизнес-запрос оператора в критерии релевантности
	// (целевой лид, признаки потребности, исключения, признаки гипотезы),
	// отдельные от технического плана обхода.
	Interpret(ctx context.Context, request InterpretRequest) (Interpretation, Usage, error)

	// Assess оценивает компанию относительно критериев релевантности задания:
	// вердикт, тип сигнала и свидетельства со ссылками на источники.
	Assess(ctx context.Context, request AssessRequest) (Assessment, Usage, error)
}

// Usage — расход токенов одного вызова LLM. Значения могут быть нулевыми, если
// провайдер не вернул учёт токенов в ответе.
type Usage struct {
	// PromptTokens — токены входа (промпт).
	PromptTokens int
	// CompletionTokens — токены выхода (ответ модели).
	CompletionTokens int
	// TotalTokens — суммарный расход.
	TotalTokens int
}

// PlanRequest — запрос оператора к планированию поиска.
type PlanRequest struct {
	// Query — текст запроса. Обязателен.
	Query string
	// Interpretation — критерии релевантности задания. Может быть пустой
	// (Uncertainty): планирование тогда опирается только на Query. Когда критерии
	// заданы, планирование обязано учитывать исключения (порталы, каталоги,
	// магазины, отдельные статьи) и признаки потребности, чтобы цели обхода вели
	// к сайтам компаний, а не к обзорным материалам.
	Interpretation Interpretation
	// StrictTargets включает строгую схему ответа нового исследовательского
	// планировщика; старый путь сохраняет совместимость по умолчанию.
	StrictTargets bool
	// MaxTargets — технический предел предложений, не денежный бюджет.
	MaxTargets int
	// QueryOnly запрещает предложения URL в фазе обнаружения компаний.
	QueryOnly bool
}

// Target — один пункт плана: либо поисковый запрос, либо стартовый URL.
type Target struct {
	// Query — поисковый запрос для порта search.
	Query string
	// URL — стартовый URL для порта crawl.
	URL string
}

// Validate проверяет обязательные поля запроса.
func (r PlanRequest) Validate() error {
	if strings.TrimSpace(r.Query) == "" {
		return fmt.Errorf("llm: plan request: query обязателен")
	}
	if r.StrictTargets && (r.MaxTargets < 1 || r.MaxTargets > 64) {
		return fmt.Errorf("llm: plan request: строгая схема требует max_targets от 1 до 64")
	}
	return nil
}

// Validate проверяет, что пункт плана задаёт ровно один способ поиска: либо
// поисковый запрос, либо стартовый URL, но не оба и не ни одного.
func (t Target) Validate() error {
	hasQuery := strings.TrimSpace(t.Query) != ""
	hasURL := strings.TrimSpace(t.URL) != ""
	if hasQuery == hasURL {
		return fmt.Errorf("llm: target: задан ровно один из query/url")
	}
	return nil
}

// SuggestRequest — описание бизнеса/задачи, по которому модель подбирает цели
// поиска.
type SuggestRequest struct {
	// Description — описание бизнеса или задачи оператора. Обязателен.
	Description string
}

// Suggestion — одно предложение: сектор, ниша и готовые seed-запросы для
// поиска.
type Suggestion struct {
	// Sector — сектор, к которому относится предложение.
	Sector string
	// Niche — ниша внутри сектора.
	Niche string
	// SeedQueries — готовые поисковые запросы для этой ниши.
	SeedQueries []string
}

// Validate проверяет обязательные поля запроса.
func (r SuggestRequest) Validate() error {
	if strings.TrimSpace(r.Description) == "" {
		return fmt.Errorf("llm: suggest request: description обязателен")
	}
	return nil
}

// Validate проверяет, что предложение задаёт сектор, нишу и хотя бы один
// непустой seed-запрос.
func (s Suggestion) Validate() error {
	var invalid []string
	if strings.TrimSpace(s.Sector) == "" {
		invalid = append(invalid, "sector")
	}
	if strings.TrimSpace(s.Niche) == "" {
		invalid = append(invalid, "niche")
	}
	hasSeed := false
	for _, q := range s.SeedQueries {
		if strings.TrimSpace(q) != "" {
			hasSeed = true
			break
		}
	}
	if !hasSeed {
		invalid = append(invalid, "seed_queries")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("llm: suggestion: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Роли сообщений диалога.
const (
	// RoleUser — сообщение оператора.
	RoleUser = "user"
	// RoleAssistant — сообщение агента.
	RoleAssistant = "assistant"
)

// Message — одно сообщение истории диалога.
type Message struct {
	// Role — роль автора: RoleUser или RoleAssistant.
	Role string
	// Content — текст сообщения. Обязателен.
	Content string
}

// Validate проверяет известную роль и непустой текст сообщения.
func (m Message) Validate() error {
	if m.Role != RoleUser && m.Role != RoleAssistant {
		return fmt.Errorf("llm: message: роль %q неизвестна", m.Role)
	}
	if strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("llm: message: content обязателен")
	}
	return nil
}

// ConverseRequest — ход диалога: история разговора на текущий момент.
type ConverseRequest struct {
	// History — история диалога в хронологическом порядке. Обязательна хотя бы
	// одним сообщением, последнее сообщение должно быть от оператора.
	History []Message
}

// Validate проверяет, что история непуста, сообщения корректны, а последнее
// сообщение принадлежит оператору.
func (r ConverseRequest) Validate() error {
	if len(r.History) == 0 {
		return fmt.Errorf("llm: converse request: history обязательна")
	}
	for i, m := range r.History {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("llm: converse request: сообщение %d: %w", i, err)
		}
	}
	if last := r.History[len(r.History)-1]; last.Role != RoleUser {
		return fmt.Errorf("llm: converse request: последнее сообщение должно быть от оператора")
	}
	return nil
}

// ConverseReply — ответ агента на ход диалога: реплика, черновик целей,
// операторские лимиты плана и признак готовности. Targets — предпросмотр для
// панели плана; авторитетный план собирается отдельно операцией Plan.
type ConverseReply struct {
	// Text — реплика агента: уточняющий вопрос либо описание плана. Обязательна.
	Text string
	// Targets — черновик целей обхода, накопленный к этому ходу. Может быть пуст.
	Targets []Target
	// Limits — согласованные в диалоге операторские лимиты обхода. Может быть пуст.
	Limits PlanLimits
	// Ready — признак, что агент считает план готовым к подтверждению оператором.
	Ready bool
}

// PlanLimits — операторские лимиты обхода, согласованные в диалоге: глубина
// обхода и предельное время работы в секундах. Число страниц и include/exclude-пути
// перестали быть операторскими: страницы задаёт внутренний предохранитель
// конфигурации, пути — внутренние значения. Согласование идёт в разговоре, а не в
// отдельной форме (design.md, Решения 5 и 6).
type PlanLimits struct {
	// MaxDepth — максимальная глубина обхода. 0 — не ограничено; отрицательное недопустимо.
	MaxDepth int
	// MaxTimeSeconds — предельное время работы в секундах. 0 — не ограничено;
	// отрицательное недопустимо.
	MaxTimeSeconds int
}

// Validate проверяет неотрицательность лимитов.
func (l PlanLimits) Validate() error {
	if l.MaxDepth < 0 {
		return fmt.Errorf("llm: plan limits: max_depth не может быть отрицательным")
	}
	if l.MaxTimeSeconds < 0 {
		return fmt.Errorf("llm: plan limits: max_time_seconds не может быть отрицательным")
	}
	return nil
}

// ConverseDelta — один кадр потока хода диалога: либо фрагмент текста реплики,
// либо завершающий кадр с готовым ответом и расходом токенов. У завершающего
// кадра Text пуст, а Reply заполнен.
type ConverseDelta struct {
	// Text — очередной фрагмент реплики. У завершающего кадра пуст.
	Text string
	// Reply — готовый ответ; заполнен только в завершающем кадре.
	Reply *ConverseReply
	// Usage — учёт токенов вызова; заполнен в завершающем кадре.
	Usage Usage
}

// Validate проверяет непустую реплику и корректность черновика целей.
func (r ConverseReply) Validate() error {
	if strings.TrimSpace(r.Text) == "" {
		return fmt.Errorf("llm: converse reply: text обязателен")
	}
	for i, t := range r.Targets {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("llm: converse reply: цель %d: %w", i, err)
		}
	}
	if err := r.Limits.Validate(); err != nil {
		return fmt.Errorf("llm: converse reply: %w", err)
	}
	return nil
}

// InterpretRequest — бизнес-запрос оператора, который превращается в критерии
// релевантности.
type InterpretRequest struct {
	// Query — текст бизнес-запроса оператора. Обязателен.
	Query string
}

// Validate проверяет непустой запрос.
func (r InterpretRequest) Validate() error {
	if strings.TrimSpace(r.Query) == "" {
		return fmt.Errorf("llm: interpret request: query обязателен")
	}
	return nil
}

// Interpretation — критерии релевантности, выведенные из бизнес-запроса: кто
// целевой лид, признаки искомой потребности, исключения и признаки гипотезы.
// Отдельная от технического плана сущность: план отвечает на «где и сколько
// искать», интерпретация — на «почему компания решает задачу».
type Interpretation struct {
	// TargetLead — кто является целевым лидом. Обязателен, если не Uncertainty.
	TargetLead string
	// NeedSignals — признаки искомой потребности. Обязателен хотя бы один
	// непустой элемент, если не Uncertainty.
	NeedSignals []string
	// Exclusions — что считается исключением. Может быть пусто.
	Exclusions []string
	// HypothesisSignals — признаки, дающие лишь гипотезу. Может быть пусто.
	HypothesisSignals []string
	// Uncertainty — недостаточно данных: система явно показывает
	// неопределённость и не выдумывает критерии.
	Uncertainty bool
	// Reason — причина, по которой критерии не определены (например, сбой вывода
	// интерпретации). Пусто, когда критерии выведены успешно. На валидность
	// интерпретации не влияет адаптивность: поле необязательно.
	Reason string
}

// Validate проверяет обязательные критерии: без явной неопределённости целевой
// лид и хотя бы один признак потребности непусты.
func (i Interpretation) Validate() error {
	if i.Uncertainty {
		return nil
	}
	var invalid []string
	if strings.TrimSpace(i.TargetLead) == "" {
		invalid = append(invalid, "target_lead")
	}
	if !hasNonEmpty(i.NeedSignals) {
		invalid = append(invalid, "need_signals")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("llm: interpretation: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// AssessMaterial — фрагмент материала о компании для оценки: источник и текст.
type AssessMaterial struct {
	// URL — адрес источника фрагмента.
	URL string
	// Text — текст фрагмента.
	Text string
}

// AssessRequest — запрос оценки компании относительно критериев задания.
type AssessRequest struct {
	// JobID — идентификатор задания. Обязателен.
	JobID string
	// CompanyName — название компании. Может быть пусто.
	CompanyName string
	// CompanyDescription — описание компании. Может быть пусто.
	CompanyDescription string
	// Interpretation — критерии релевантности задания.
	Interpretation Interpretation
	// Material — собранный материал о компании. Может быть пуст.
	Material []AssessMaterial
}

// Validate проверяет обязательный идентификатор задания.
func (r AssessRequest) Validate() error {
	if strings.TrimSpace(r.JobID) == "" {
		return fmt.Errorf("llm: assess request: job_id обязателен")
	}
	return nil
}

// Вердикты оценки компании относительно задания.
const (
	// VerdictFits — компания подходит запросу.
	VerdictFits = "fits"
	// VerdictHypothesis — компания подходит по профилю, но сигнала потребности нет.
	VerdictHypothesis = "hypothesis"
	// VerdictUnsuitable — установлено несоответствие запросу.
	VerdictUnsuitable = "unsuitable"
)

// Типы сигнала оценки.
const (
	// SignalDirect — прямо заявленная потребность.
	SignalDirect = "direct"
	// SignalIndirect — косвенный наблюдаемый сигнал.
	SignalIndirect = "indirect"
	// SignalProfile — совпадение по профилю без сигнала потребности.
	SignalProfile = "profile"
)

// AssessmentEvidence — одно свидетельство оценки: критерий из запроса,
// наблюдаемый факт, ссылка на источник и объяснение связи факта с потребностью.
// Факт, интерпретация и неподтверждённое намерение купить — раздельные поля, а не
// один текст.
type AssessmentEvidence struct {
	// Criterion — критерий из интерпретации запроса. Обязателен.
	Criterion string
	// Fact — наблюдаемый факт. Обязателен.
	Fact string
	// SourceURL — ссылка на источник, подтверждающий факт. Обязательна.
	SourceURL string
	// Reasoning — объяснение связи факта с потребностью. Обязательно.
	Reasoning string
	// SignalKind — тип сигнала: SignalDirect, SignalIndirect или SignalProfile.
	SignalKind string
	// Missing — какого свидетельства не хватает (для гипотезы). Может быть пусто.
	Missing string
	// Mismatch — установленное несоответствие (для «Не подходит»). Может быть пусто.
	Mismatch string
}

// Validate проверяет обязательные поля свидетельства и известность типа сигнала.
func (e AssessmentEvidence) Validate() error {
	var invalid []string
	if strings.TrimSpace(e.Criterion) == "" {
		invalid = append(invalid, "criterion")
	}
	if strings.TrimSpace(e.Fact) == "" {
		invalid = append(invalid, "fact")
	}
	if strings.TrimSpace(e.SourceURL) == "" {
		invalid = append(invalid, "source_url")
	}
	if strings.TrimSpace(e.Reasoning) == "" {
		invalid = append(invalid, "reasoning")
	}
	switch e.SignalKind {
	case SignalDirect, SignalIndirect, SignalProfile:
	default:
		invalid = append(invalid, "signal_kind")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("llm: assessment evidence: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Assessment — оценка компании относительно критериев задания: вердикт, тип
// сигнала и свидетельства. Оценка привязана к заданию и не является постоянным
// свойством компании.
type Assessment struct {
	// Verdict — вердикт: VerdictFits, VerdictHypothesis или VerdictUnsuitable.
	Verdict string
	// SignalKind — тип сигнала оценки: SignalDirect, SignalIndirect или SignalProfile.
	SignalKind string
	// Evidence — свидетельства оценки. Для «Подходит» и «Гипотезы» обязательны.
	Evidence []AssessmentEvidence
}

// Validate ступенчато проверяет вердикт, сочетание вердикта с типом сигнала и
// требования к свидетельствам: «Подходит» требует прямого или косвенного сигнала
// и свидетельства с критерием, фактом, источником и обоснованием; «Гипотеза» —
// профиля и свидетельства с недостающим признаком; «Не подходит» — свидетельства
// с установленным несоответствием.
func (a Assessment) Validate() error {
	for i, e := range a.Evidence {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("llm: assessment: свидетельство %d: %w", i, err)
		}
	}
	switch a.Verdict {
	case VerdictFits:
		if a.SignalKind != SignalDirect && a.SignalKind != SignalIndirect {
			return fmt.Errorf("llm: assessment: вердикт %q требует signal_kind %q или %q, получен %q",
				VerdictFits, SignalDirect, SignalIndirect, a.SignalKind)
		}
		for _, e := range a.Evidence {
			if strings.TrimSpace(e.Criterion) != "" && strings.TrimSpace(e.Fact) != "" &&
				strings.TrimSpace(e.SourceURL) != "" && strings.TrimSpace(e.Reasoning) != "" {
				return nil
			}
		}
		return fmt.Errorf("llm: assessment: вердикт %q требует свидетельство с непустыми criterion, fact, source_url и reasoning", VerdictFits)
	case VerdictHypothesis:
		if a.SignalKind != SignalProfile {
			return fmt.Errorf("llm: assessment: вердикт %q требует signal_kind %q, получен %q",
				VerdictHypothesis, SignalProfile, a.SignalKind)
		}
		for _, e := range a.Evidence {
			if strings.TrimSpace(e.Missing) != "" {
				return nil
			}
		}
		return fmt.Errorf("llm: assessment: вердикт %q требует свидетельство с непустым missing", VerdictHypothesis)
	case VerdictUnsuitable:
		for _, e := range a.Evidence {
			if strings.TrimSpace(e.Mismatch) != "" {
				return nil
			}
		}
		return fmt.Errorf("llm: assessment: вердикт %q требует свидетельство с непустым mismatch", VerdictUnsuitable)
	case "":
		return fmt.Errorf("llm: assessment: verdict обязателен")
	default:
		return fmt.Errorf("llm: assessment: неизвестный verdict %q", a.Verdict)
	}
}

// hasNonEmpty сообщает, есть ли в списке хотя бы одна непустая (не из пробелов)
// строка.
func hasNonEmpty(values []string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}
