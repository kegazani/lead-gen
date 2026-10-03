package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// ErrInvalidSearchCriteria — критерий поиска лидов некорректен (пуст или лимит
// отрицателен). Владелец не выдумывает результат по несформулированному запросу.
var ErrInvalidSearchCriteria = errors.New("app: критерий поиска лидов некорректен")

// Пределы и умолчания синхронного поиска под заявку. Значения — предохранители
// расхода Firecrawl в синхронном пути, а не бизнес-параметры.
const (
	// DefaultSearchLeadsLimit — сколько лидов ищется, если лимит не задан.
	DefaultSearchLeadsLimit = 10
	// DefaultSearchLeadsMaxResults — сколько верхних результатов Firecrawl-поиска
	// обходится за один синхронный поиск (топ-N сайтов).
	DefaultSearchLeadsMaxResults = 5
	// DefaultSearchLeadsDeadline — предельное время фазы краулинга целиком.
	DefaultSearchLeadsDeadline = 30 * time.Second
	// DefaultSearchLeadsMaxPages — предельное число страниц обхода одного сайта.
	DefaultSearchLeadsMaxPages = 5
	// DefaultSearchLeadsSiteTimeSeconds — предельное время обхода одного сайта.
	DefaultSearchLeadsSiteTimeSeconds = 15
	// poolScanLimit — верхняя граница карточек пула, читаемых для подбора. Пул
	// клиента в срезе лёгкий: отдельного индексного поиска по критерию в портах
	// нет, поэтому подбор идёт по вычитке витрины (как воронка).
	poolScanLimit = 10000
	// minCriteriaTokenRunes — минимальная длина значимого слова критерия. Короткие
	// слова (предлоги, союзы) в подбор не берутся: они дают ложные совпадения.
	minCriteriaTokenRunes = 3
	// strongCriteriaTokenRunes — длина слова, при которой одиночного совпадения
	// достаточно для матча: длинное конкретное слово («разработка», «логистика»)
	// несёт смысл само по себе. Более короткие значимые слова матчат только
	// парами — это отсекает случайное пересечение по одному общему слову.
	strongCriteriaTokenRunes = 5
	// minMatchedCriteriaTokens — сколько различных значимых слов критерия должно
	// встретиться в карточке, чтобы матч считался осмысленным. Порог в два слова
	// (в сочетании со strongCriteriaTokenRunes) не даёт несвязанной карточке
	// пройти по единственному случайно общему слову.
	minMatchedCriteriaTokens = 2
)

// stopWords — служебные слова, не несущие смысла для подбора: русские
// предлоги/союзы/местоимения и англ. служебные слова. Без их отсева любая
// карточка с «для»/«и»/«the» в описании засчитывалась как матч по несвязанному
// запросу. Держим список уникальным: дубликат ключа в литерале — ошибка компиляции.
var stopWords = map[string]struct{}{
	// Русские предлоги, союзы, частицы, местоимения.
	"для": {}, "что": {}, "чтобы": {}, "как": {}, "так": {}, "это": {}, "этот": {},
	"эта": {}, "эти": {}, "его": {}, "ее": {}, "её": {}, "их": {}, "еще": {}, "ещё": {},
	"уже": {}, "или": {}, "если": {}, "когда": {}, "где": {}, "куда": {}, "зачем": {},
	"при": {}, "над": {}, "под": {}, "про": {}, "без": {}, "через": {}, "между": {},
	"можно": {}, "нельзя": {}, "надо": {}, "есть": {}, "был": {}, "была": {}, "были": {},
	"быть": {}, "будет": {}, "весь": {}, "все": {}, "всё": {}, "вся": {}, "кто": {},
	"чем": {}, "то": {}, "же": {}, "ли": {}, "бы": {}, "не": {}, "ни": {}, "но": {},
	"да": {}, "нет": {}, "он": {}, "она": {}, "они": {}, "оно": {}, "мы": {}, "вы": {},
	"ты": {}, "я": {}, "мне": {}, "вам": {}, "нам": {}, "вас": {}, "нас": {}, "себя": {},
	"себе": {}, "свой": {}, "свою": {}, "также": {}, "тоже": {}, "лишь": {}, "только": {},
	"более": {}, "менее": {}, "очень": {}, "самый": {}, "такой": {}, "такая": {},
	"такие": {}, "какой": {}, "какая": {}, "какие": {}, "который": {}, "которая": {},
	"которые": {}, "которое": {}, "чему": {}, "кому": {}, "кого": {}, "чего": {},
	"по": {}, "из": {}, "от": {}, "до": {}, "со": {}, "во": {}, "об": {}, "о": {},
	"им": {}, "ему": {}, "ей": {},
	// Англ. служебные слова.
	"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "that": {}, "this": {},
	"these": {}, "those": {}, "are": {}, "was": {}, "were": {}, "will": {}, "can": {},
	"could": {}, "would": {}, "should": {}, "not": {}, "but": {}, "you": {}, "your": {},
	"our": {}, "their": {}, "his": {}, "her": {}, "its": {}, "have": {}, "has": {},
	"had": {}, "they": {}, "them": {}, "then": {}, "than": {}, "into": {}, "onto": {},
	"out": {}, "over": {}, "under": {}, "about": {}, "also": {}, "more": {}, "most": {},
	"some": {}, "such": {}, "any": {}, "all": {}, "one": {}, "two": {}, "of": {},
	"to": {}, "in": {}, "on": {}, "at": {}, "by": {}, "as": {}, "an": {}, "is": {},
	"be": {}, "or": {}, "if": {}, "it": {}, "we": {}, "he": {}, "she": {}, "do": {},
	"does": {}, "did": {}, "so": {}, "no": {}, "up": {}, "down": {}, "just": {},
	"only": {}, "very": {}, "each": {}, "other": {}, "been": {}, "being": {},
}

// LeadOrigin — происхождение найденного лида.
type LeadOrigin string

const (
	// LeadOriginPool — лид взят из существующего пула.
	LeadOriginPool LeadOrigin = "pool"
	// LeadOriginCrawl — лид добрался краулингом и сохранён.
	LeadOriginCrawl LeadOrigin = "crawl"
)

// SearchLeadsInput — вход сценария поиска лидов под заявку.
type SearchLeadsInput struct {
	// RequestID — заявка, под которую ищутся лиды.
	RequestID string
	// TenantID — тенант-владелец пула.
	TenantID string
	// Criteria — критерий поиска (текст оффера/цели клиента). Обязателен.
	Criteria string
	// Limit — сколько лидов нужно; 0 — значение по умолчанию, отрицательное
	// запрещено.
	Limit int
}

// SearchLeadRef — ссылка на найденный лид: идентификатор компании и происхождение.
type SearchLeadRef struct {
	CompanyID string
	Origin    LeadOrigin
}

// SearchLeadsResult — результат поиска: сколько найдено и откуда.
type SearchLeadsResult struct {
	Found    int
	FromPool int
	Crawled  int
	Leads    []SearchLeadRef
}

// SearchLeadsForRequest — use case поиска лидов под заявку: сначала берёт
// кандидатов из существующего пула (карточки компаний), а при нехватке добирает
// ограниченным краулингом Firecrawl и сохраняет новых.
//
// Подбор по пулу — структурированный/ключевой: домены карточек сопоставляются со
// значимыми словами критерия. Семантический recall (SemanticRecall) здесь не
// используется: он требует векторного хранилища (vector.Port), которого у сервиса
// нет, и молчаливая деградация до «пусто» скрыла бы, что recall не смонтирован.
// Смонтированный recall — отдельное расширение, подменяющее фазу подбора.
type SearchLeadsForRequest struct {
	// Profiles — репозиторий карточек компаний. Обязателен.
	Profiles ports.LeadProfileRepository
	// Contacts — репозиторий контактов; нужен, чтобы сохранять контакты найденных
	// краулингом компаний.
	Contacts ports.ContactRepository
	// Search — порт поиска (Firecrawl /search). nil — дозабор краулингом
	// недоступен, поиск отвечает по пулу.
	Search search.Port
	// Crawl — порт обхода. nil — дозабор краулингом недоступен.
	Crawl crawl.Port
	// Clock — часы; nil — системное время.
	Clock ports.Clock
	// Limits — пределы обхода одного сайта.
	Limits CrawlLimits
	// MaxResults — верхняя граница результатов Firecrawl-поиска (топ-N сайтов) за
	// один вызов. 0 — DefaultSearchLeadsMaxResults.
	MaxResults int
	// Deadline — предельное время фазы краулинга. 0 — DefaultSearchLeadsDeadline.
	Deadline time.Duration
	// Logger — логгер; nil — без логов.
	Logger observ.Logger
	// Tracer — трейсер; nil — без спанов.
	Tracer observ.Tracer
	// ServiceName — имя сервиса для полей наблюдаемости.
	ServiceName string
}

// Run исполняет поиск: подбор из пула, при нехватке — ограниченный краулинг с
// сохранением новых. Ошибка чтения пула возвращается вызывающему; сбой поиска или
// обхода не роняет результат — возвращается то, что уже найдено в пуле, а причина
// пишется в лог (потребитель видит недобор по счётчикам Crawled/Found).
func (u *SearchLeadsForRequest) Run(ctx context.Context, in SearchLeadsInput) (SearchLeadsResult, error) {
	if u.Profiles == nil {
		return SearchLeadsResult{}, fmt.Errorf("%w: репозиторий карточек не подключён", ErrInvalidSearchCriteria)
	}
	criteria := strings.TrimSpace(in.Criteria)
	if criteria == "" {
		return SearchLeadsResult{}, fmt.Errorf("%w: критерий поиска пуст", ErrInvalidSearchCriteria)
	}
	if in.Limit < 0 {
		return SearchLeadsResult{}, fmt.Errorf("%w: лимит не может быть отрицательным: %d", ErrInvalidSearchCriteria, in.Limit)
	}
	limit := u.limit(in.Limit)

	ctx, finish := u.startSpan(ctx, "lead-search")

	companies, err := u.Profiles.ListVisible(ctx, poolScanLimit)
	if err != nil {
		finish(err)
		return SearchLeadsResult{}, fmt.Errorf("app: чтение пула компаний: %w", err)
	}

	seen := make(map[string]struct{}, limit)
	leads := make([]SearchLeadRef, 0, limit)
	for _, company := range poolMatches(criteria, companies, limit) {
		id := company.ID().String()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		leads = append(leads, SearchLeadRef{CompanyID: id, Origin: LeadOriginPool})
	}
	fromPool := len(leads)
	if fromPool >= limit {
		finish(nil)
		return newSearchLeadsResult(leads, fromPool, 0), nil
	}

	crawled := u.crawlDeficit(ctx, criteria, limit-fromPool, seen, &leads)
	finish(nil)
	return newSearchLeadsResult(leads, fromPool, crawled), nil
}

// newSearchLeadsResult собирает результат из накопленных ссылок.
func newSearchLeadsResult(leads []SearchLeadRef, fromPool, crawled int) SearchLeadsResult {
	return SearchLeadsResult{
		Found:    len(leads),
		FromPool: fromPool,
		Crawled:  crawled,
		Leads:    leads,
	}
}

// crawlDeficit добирает недостающих лидов ограниченным краулингом Firecrawl.
// Возвращает число добавленных краулингом лидов. Дозабор не запускается, если
// порты поиска/обхода не подключены, и не роняет результат при сбое движка.
func (u *SearchLeadsForRequest) crawlDeficit(ctx context.Context, criteria string, need int, seen map[string]struct{}, leads *[]SearchLeadRef) int {
	if u.Search == nil || u.Crawl == nil || need <= 0 {
		return 0
	}
	top := u.maxResults()
	if top > need {
		top = need
	}
	if top <= 0 {
		return 0
	}

	crawlCtx := ctx
	if deadline := u.deadline(); deadline > 0 {
		var cancel context.CancelFunc
		crawlCtx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}

	results, err := u.Search.Search(crawlCtx, search.Query{Text: criteria, MaxResults: top})
	if err != nil {
		// Сбой поиска — не выдуманный «пусто»: результат остаётся пуловым, причина
		// видна в логе и в счётчике Crawled.
		u.warn("поиск под заявку: поисковый запрос не удался", "query", criteria, "error", err.Error())
		return 0
	}

	added := 0
	for _, res := range results {
		if added >= need || crawlCtx.Err() != nil {
			break
		}
		if strings.TrimSpace(res.URL) == "" {
			continue
		}
		added += u.crawlSite(crawlCtx, res.URL, need-added, seen, leads)
	}
	return added
}

// crawlSite обходит один сайт и сохраняет новые компании страниц. Отмена или
// исчерпание времени не считаются сбоем: уже собранные страницы сохраняются.
func (u *SearchLeadsForRequest) crawlSite(ctx context.Context, startURL string, need int, seen map[string]struct{}, leads *[]SearchLeadRef) int {
	outcome, err := u.Crawl.Crawl(ctx, u.crawlRequest(startURL))
	if err != nil && !errors.Is(err, crawl.ErrCancelled) {
		u.warn("поиск под заявку: обход сайта не удался", "url", startURL, "error", err.Error())
		return 0
	}

	added := 0
	for _, page := range outcome.Pages {
		if added >= need || ctx.Err() != nil {
			break
		}
		id, ok := u.savePage(ctx, page, seen)
		if !ok {
			continue
		}
		*leads = append(*leads, SearchLeadRef{CompanyID: id, Origin: LeadOriginCrawl})
		added++
	}
	return added
}

// savePage разбирает одну страницу в карточку компании и сохраняет её.
// Возвращает идентификатор сохранённой компании и признак успеха. Домен, уже
// попавший в результат (в том числе из пула), не добавляется повторно.
func (u *SearchLeadsForRequest) savePage(ctx context.Context, page crawl.Page, seen map[string]struct{}) (string, bool) {
	at := u.now()
	name, description, socials := extractCard(page, at)
	contacts := extractContacts(page, at)

	// Эскалация на структурированную экстракцию — как в плановом обходе: при
	// неполной карточке пробуем extract, ошибка не фатальна.
	if !isFullCard(name, description, contacts) {
		candidates, err := u.Crawl.Extract(ctx, crawl.ExtractRequest{URL: page.URL})
		if err == nil {
			contacts = mergeContacts(contacts, contactCandidatesFromExtract(candidates, page.URL, at))
		} else {
			u.warn("поиск под заявку: эскалация на extract не удалась", "url", page.URL, "error", err.Error())
		}
	}

	identity := NormalizeDomain(page.URL)
	if name.Value == "" || identity == "" {
		return "", false
	}
	if _, ok := seen[identity]; ok {
		return "", false
	}
	if err := persistCompany(ctx, u.Profiles, u.Contacts, identity, name, description, socials, contacts, at); err != nil {
		u.error("поиск под заявку: сохранение компании", err, "url", page.URL)
		return "", false
	}
	seen[identity] = struct{}{}
	return identity, true
}

// crawlRequest собирает параметры обхода одного сайта с предохранителями по
// умолчанию: синхронный путь не должен опираться на отсутствующий лимит.
func (u *SearchLeadsForRequest) crawlRequest(startURL string) crawl.Request {
	limits := u.Limits
	if limits.MaxPages <= 0 {
		limits.MaxPages = DefaultSearchLeadsMaxPages
	}
	if limits.MaxDepth < 0 {
		limits.MaxDepth = 0
	}
	if limits.MaxTimeSeconds <= 0 {
		limits.MaxTimeSeconds = DefaultSearchLeadsSiteTimeSeconds
	}
	return crawl.Request{
		StartURL:       startURL,
		MaxPages:       limits.MaxPages,
		MaxDepth:       limits.MaxDepth,
		MaxTimeSeconds: limits.MaxTimeSeconds,
		IncludePaths:   limits.IncludePaths,
		ExcludePaths:   limits.ExcludePaths,
	}
}

// poolMatches подбирает карточки пула под критерий: значимые слова критерия
// сопоставляются с текстом карточки (название, описание, соцсети). Матч
// засчитывается по qualifiesMatch: минимум два различных значимых слова либо
// одно «сильное» слово. Ранжирование — по числу совпавших слов, затем по
// идентификатору (детерминированно).
func poolMatches(criteria string, companies []*domain.Company, limit int) []*domain.Company {
	tokens := criteriaTokens(criteria)
	if len(tokens) == 0 {
		return nil
	}
	type scored struct {
		company *domain.Company
		score   int
	}
	var matches []scored
	for _, company := range companies {
		matched := matchedCriteriaTokens(tokens, companySearchText(company))
		if !qualifiesMatch(matched) {
			continue
		}
		matches = append(matches, scored{company: company, score: len(matched)})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].company.ID() < matches[j].company.ID()
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]*domain.Company, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.company)
	}
	return out
}

// companySearchText собирает текст карточки для подбора: название, описание и
// соцсети в нижнем регистре.
func companySearchText(company *domain.Company) string {
	var b strings.Builder
	b.WriteString(company.Name().Value)
	b.WriteString(" ")
	b.WriteString(company.Description().Value)
	for _, social := range company.Socials() {
		b.WriteString(" ")
		b.WriteString(social.Value)
	}
	return strings.ToLower(b.String())
}

// criteriaTokens выделяет значимые слова критерия: буквы и цифры в нижнем
// регистре, служебные (stopWords) и короткие (< minCriteriaTokenRunes) слова
// отбрасываются, дубли убираются. Пустой результат означает, что критерий не
// содержит значимых слов, — такой запрос не матчит пул (уходит в краулинг).
func criteriaTokens(criteria string) []string {
	raw := strings.FieldsFunc(criteria, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, field := range raw {
		token := strings.ToLower(strings.TrimSpace(field))
		if utf8.RuneCountInString(token) < minCriteriaTokenRunes {
			continue
		}
		if _, ok := stopWords[token]; ok {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

// matchedCriteriaTokens возвращает значимые слова критерия, встретившиеся в
// тексте карточки как подстроки (порядок тот же, что и в criteriaTokens).
func matchedCriteriaTokens(tokens []string, text string) []string {
	var matched []string
	for _, token := range tokens {
		if strings.Contains(text, token) {
			matched = append(matched, token)
		}
	}
	return matched
}

// qualifiesMatch решает, достаточно ли найденных значимых слов для матча.
// Порог: два различных слова — надёжное пересечение даже для коротких слов;
// одно слово засчитывается только если оно длинное (strongCriteriaTokenRunes),
// то есть достаточно конкретное. Стоп-слова и одиночные короткие общие слова
// (главная причина ложных матчей) сюда не проходят.
func qualifiesMatch(matched []string) bool {
	if len(matched) >= minMatchedCriteriaTokens {
		return true
	}
	return len(matched) == 1 && utf8.RuneCountInString(matched[0]) >= strongCriteriaTokenRunes
}

// limit возвращает действующий лимит лидов.
func (u *SearchLeadsForRequest) limit(limit int) int {
	if limit > 0 {
		return limit
	}
	return DefaultSearchLeadsLimit
}

// maxResults возвращает верхнюю границу результатов поиска (топ-N сайтов).
func (u *SearchLeadsForRequest) maxResults() int {
	if u.MaxResults > 0 {
		return u.MaxResults
	}
	return DefaultSearchLeadsMaxResults
}

// deadline возвращает предельное время фазы краулинга.
func (u *SearchLeadsForRequest) deadline() time.Duration {
	if u.Deadline > 0 {
		return u.Deadline
	}
	return DefaultSearchLeadsDeadline
}

// now возвращает время часов либо системное.
func (u *SearchLeadsForRequest) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// startSpan открывает спан операции; без трейсера возвращает контекст и пустую
// функцию завершения.
func (u *SearchLeadsForRequest) startSpan(ctx context.Context, name string) (context.Context, func(error)) {
	if u.Tracer == nil {
		return ctx, func(error) {}
	}
	return u.Tracer.Start(ctx, name)
}

// warn пишет предупреждение с полями наблюдаемости.
func (u *SearchLeadsForRequest) warn(message string, attrs ...any) {
	if u.Logger == nil {
		return
	}
	u.Logger.Warn(message, observ.Fields{}, append([]any{"service", u.ServiceName}, attrs...)...)
}

// error пишет ошибку с полями наблюдаемости.
func (u *SearchLeadsForRequest) error(message string, err error, attrs ...any) {
	if u.Logger == nil {
		return
	}
	u.Logger.Error(message, err, observ.Fields{}, append([]any{"service", u.ServiceName}, attrs...)...)
}
