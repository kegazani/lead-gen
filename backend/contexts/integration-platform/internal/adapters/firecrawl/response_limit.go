package firecrawl

import (
	"context"
	"errors"
)

type responseLimitKey struct{}

// ErrResponseTooLarge сообщает о техническом пределе ответа. Запрос уже мог
// быть оплачен; guard не должен считать эту ошибку доказательством бесплатности.
var ErrResponseTooLarge = errors.New("firecrawl: ответ превышает технический предел")

// WithResponseLimit задаёт конечный предел wire-response для нового bounded
// research пути. Legacy вызовы без этого контекста сохраняют прежнюю семантику.
func WithResponseLimit(ctx context.Context, maxBytes int64) context.Context {
	return context.WithValue(ctx, responseLimitKey{}, maxBytes)
}
