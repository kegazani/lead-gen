package app

import (
	"context"
	"strings"

	"lead-gen/contracts/integration-platform/forum"
)

// SearchForumSignals — use case поиска обезличенных форумных сигналов: запрос →
// сигналы через forum.Port с анонимизацией (исключение материалов с прямыми
// идентификаторами участников).
type SearchForumSignals struct {
	Forum forum.Port
}

// Search возвращает обезличенные сигналы по запросу.
func (s *SearchForumSignals) Search(ctx context.Context, query string) ([]forum.Signal, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errEmptyQuery
	}
	signals, err := s.Forum.SearchSignals(ctx, forum.Query{Text: query})
	if err != nil {
		return nil, err
	}
	return anonymizeSignals(signals), nil
}

// anonymizeSignals исключает материалы, содержащие прямые идентификаторы
// участников. Имена как идентификаторы требуют NLP и в этом срезе не
// размечаются; обезличенность гарантируется по детектируемым контактам —
// адресам и телефонам, извлекаемым теми же правилами, что и при обогащении.
func anonymizeSignals(signals []forum.Signal) []forum.Signal {
	out := make([]forum.Signal, 0, len(signals))
	for _, signal := range signals {
		if containsDirectIdentifier(signal.Topic) || containsDirectIdentifier(signal.Summary) {
			continue
		}
		out = append(out, signal)
	}
	return out
}

// containsDirectIdentifier сообщает, содержит ли текст прямой контактный
// идентификатор участника (email или телефон).
func containsDirectIdentifier(text string) bool {
	return len(ExtractEmails(text)) > 0 || len(ExtractPhones(text)) > 0
}
