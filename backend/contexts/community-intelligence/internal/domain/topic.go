package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DefaultMaxQuotesPerTopic — сколько обезличенных цитат-примеров хранит тема,
// если вызывающий не задал иное.
const DefaultMaxQuotesPerTopic = 3

// MessageRef — ссылка на сообщение-источник пункта выжимки. Несёт сообщество и
// идентификатор сообщения, но не автора: связь выжимки с источником сохраняется
// без идентифицирующих данных (spec, «Прослеживаемость источника»).
type MessageRef struct {
	Community CommunityID
	Message   MessageID
}

// NewMessageRef создаёт ссылку, требуя непустые сообщество и сообщение.
func NewMessageRef(community CommunityID, message MessageID) (MessageRef, error) {
	if err := community.validate(); err != nil {
		return MessageRef{}, err
	}
	if err := message.validate(); err != nil {
		return MessageRef{}, err
	}
	return MessageRef{Community: community, Message: message}, nil
}

// Quote — короткая обезличенная цитата из сообщения. Текст уже прошёл
// обезличивание (Sanitize): упоминания и контакты заменены, авторов нет.
type Quote struct {
	text string
}

// NewQuote создаёт цитату из текста, применяя обезличивание. Пустой после
// обезличивания текст отвергается.
func NewQuote(text string) (Quote, error) {
	clean := Sanitize(text)
	if clean == "" {
		return Quote{}, ErrEmptyQuote
	}
	return Quote{text: clean}, nil
}

// Text возвращает обезличенный текст цитаты.
func (q Quote) Text() string { return q.text }

// Topic — тема выжимки: название, частота упоминаний, примеры формулировок и
// ссылки на сообщения-источники. Авторов здесь нет по построению.
type Topic struct {
	name     string
	mentions int
	quotes   []Quote
	sources  []MessageRef
}

// NewTopicParams — данные темы.
type NewTopicParams struct {
	// Name — название (представительная формулировка темы), непустое.
	Name string
	// Mentions — число упоминаний, положительное.
	Mentions int
	// Quotes — обезличенные примеры формулировок.
	Quotes []Quote
	// Sources — ссылки на сообщения-источники.
	Sources []MessageRef
}

// NewTopic создаёт тему с проверкой инвариантов.
func NewTopic(p NewTopicParams) (Topic, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Topic{}, ErrEmptyTopic
	}
	if p.Mentions <= 0 {
		return Topic{}, fmt.Errorf("%w: тема %q: %d", ErrInvalidMentions, name, p.Mentions)
	}
	quotes := make([]Quote, 0, len(p.Quotes))
	for _, q := range p.Quotes {
		if strings.TrimSpace(q.text) == "" {
			return Topic{}, ErrEmptyQuote
		}
		quotes = append(quotes, q)
	}
	sources := make([]MessageRef, 0, len(p.Sources))
	seen := make(map[MessageRef]struct{}, len(p.Sources))
	for _, ref := range p.Sources {
		if _, err := NewMessageRef(ref.Community, ref.Message); err != nil {
			return Topic{}, err
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		sources = append(sources, ref)
	}
	return Topic{name: name, mentions: p.Mentions, quotes: quotes, sources: sources}, nil
}

// Name возвращает название темы.
func (t Topic) Name() string { return t.name }

// Mentions возвращает число упоминаний.
func (t Topic) Mentions() int { return t.mentions }

// Quotes возвращает копию обезличенных примеров формулировок.
func (t Topic) Quotes() []Quote {
	out := make([]Quote, len(t.quotes))
	copy(out, t.quotes)
	return out
}

// Sources возвращает копию ссылок на сообщения-источники.
func (t Topic) Sources() []MessageRef {
	out := make([]MessageRef, len(t.sources))
	copy(out, t.sources)
	return out
}

// Candidate — обезличенный вход формирования темы: текст сообщения и ссылка на
// него. Автора здесь нет: он отделяется на границе порта и в домен не попадает
// (design.md, Решение 6).
type Candidate struct {
	// Text — текст сообщения.
	Text string
	// Ref — ссылка на сообщение-источник.
	Ref MessageRef
}

// BuildTopicsOptions — параметры политики digest-anonymization.
type BuildTopicsOptions struct {
	// CommunitySize — размер сообщества. Цитаты включаются только при размере не
	// ниже QuoteThreshold: короткая фраза из малого сообщества позволяет узнать
	// автора (design.md, Риски).
	CommunitySize int

	// QuoteThreshold — порог размера сообщества для включения цитат. Значение
	// меньше нуля означает отсутствие порога.
	QuoteThreshold int

	// MaxQuotesPerTopic — предел примеров на тему. Не положительное значение
	// заменяется на DefaultMaxQuotesPerTopic.
	MaxQuotesPerTopic int
}

// BuildTopics формирует темы выжимки из обезличенных кандидатов.
//
// Политика: однотипные формулировки (совпадающий нормализованный текст)
// объединяются в одну тему с числом повторений; представителем темы становится
// первая встреченная формулировка; примеры цитат обезличиваются и включаются
// только при достигнутом пороге сообщества. Порядок тем детерминирован: по
// убыванию числа упоминаний, затем по названию.
//
// Если пригодных кандидатов нет, возвращается ErrNoMessages: пустая выжимка с
// нулями не создаётся.
func BuildTopics(candidates []Candidate, opts BuildTopicsOptions) ([]Topic, error) {
	if opts.MaxQuotesPerTopic <= 0 {
		opts.MaxQuotesPerTopic = DefaultMaxQuotesPerTopic
	}
	quotesAllowed := opts.QuoteThreshold < 0 || opts.CommunitySize >= opts.QuoteThreshold

	type aggregate struct {
		name     string
		mentions int
		quotes   []Quote
		sources  []MessageRef
	}
	byKey := make(map[string]*aggregate)
	keys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		key := normalizeTopicKey(c.Text)
		if key == "" {
			continue
		}
		ref, err := NewMessageRef(c.Ref.Community, c.Ref.Message)
		if err != nil {
			return nil, err
		}
		acc, ok := byKey[key]
		if !ok {
			name := Sanitize(c.Text)
			if name == "" {
				name = strings.TrimSpace(c.Text)
			}
			if name == "" {
				continue
			}
			acc = &aggregate{name: name}
			byKey[key] = acc
			keys = append(keys, key)
		}
		acc.mentions++
		acc.sources = append(acc.sources, ref)
		if quotesAllowed && len(acc.quotes) < opts.MaxQuotesPerTopic {
			if quote, err := NewQuote(c.Text); err == nil {
				acc.quotes = append(acc.quotes, quote)
			}
		}
	}
	if len(keys) == 0 {
		return nil, ErrNoMessages
	}

	topics := make([]Topic, 0, len(keys))
	for _, key := range keys {
		acc := byKey[key]
		topic, err := NewTopic(NewTopicParams{
			Name:     acc.name,
			Mentions: acc.mentions,
			Quotes:   acc.quotes,
			Sources:  acc.sources,
		})
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	sort.SliceStable(topics, func(i, j int) bool {
		if topics[i].mentions != topics[j].mentions {
			return topics[i].mentions > topics[j].mentions
		}
		return topics[i].name < topics[j].name
	})
	return topics, nil
}

// normalizeTopicKey приводит текст к ключу группировки однотипных формулировок:
// нижний регистр, схлопнутые пробелы, без завершающей пунктуации.
func normalizeTopicKey(text string) string {
	lowered := strings.ToLower(strings.TrimSpace(text))
	fields := strings.Fields(lowered)
	joined := strings.Join(fields, " ")
	joined = strings.Trim(joined, " \t\r\n.,!?;:—-")
	return joined
}

var (
	reURL    = regexp.MustCompile(`(?i)\b(?:https?://|www\.|t\.me/)\S+`)
	reHandle = regexp.MustCompile(`@[A-Za-z0-9_]{3,}`)
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	rePhone  = regexp.MustCompile(`\+?\d[\d\-\s]{7,}\d`)
)

// Sanitize обезличивает текст сообщения: каналы-ссылки, упоминания, адреса и
// телефоны заменяются обобщёнными маркерами, пробелы схлопываются. Это не
// заменяет решение о пороге сообщества, но снимает очевидные идентификаторы из
// примеров формулировок.
func Sanitize(text string) string {
	out := strings.TrimSpace(text)
	out = reURL.ReplaceAllString(out, "[ссылка]")
	out = reEmail.ReplaceAllString(out, "[контакт]")
	out = reHandle.ReplaceAllString(out, "[участник]")
	out = rePhone.ReplaceAllString(out, "[контакт]")
	out = strings.Join(strings.Fields(out), " ")
	return out
}

// TopicSnapshot — сохранённое состояние темы.
type TopicSnapshot struct {
	Name     string
	Mentions int
	Quotes   []string
	Sources  []MessageRef
}

// Snapshot возвращает состояние темы для сохранения.
func (t Topic) Snapshot() TopicSnapshot {
	quotes := make([]string, 0, len(t.quotes))
	for _, q := range t.quotes {
		quotes = append(quotes, q.text)
	}
	sources := make([]MessageRef, len(t.sources))
	copy(sources, t.sources)
	return TopicSnapshot{Name: t.name, Mentions: t.mentions, Quotes: quotes, Sources: sources}
}

// RehydrateTopic восстанавливает тему из снимка, повторно проверяя инварианты.
func RehydrateTopic(s TopicSnapshot) (Topic, error) {
	quotes := make([]Quote, 0, len(s.Quotes))
	for _, text := range s.Quotes {
		if strings.TrimSpace(text) == "" {
			return Topic{}, fmt.Errorf("%w: тема %q: пустая цитата", ErrInvalidSnapshot, s.Name)
		}
		quotes = append(quotes, Quote{text: text})
	}
	topic, err := NewTopic(NewTopicParams{
		Name:     s.Name,
		Mentions: s.Mentions,
		Quotes:   quotes,
		Sources:  s.Sources,
	})
	if err != nil {
		return Topic{}, fmt.Errorf("%w: тема: %v", ErrInvalidSnapshot, err)
	}
	return topic, nil
}
