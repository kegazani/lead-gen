package telegram

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// Unavailable — адаптер порта ports.CommunityReader, честно сообщающий о
// недоступности источника: авторизованная сессия не подключена.
//
// Адаптер ничего не читает и не ходит в сеть. Любой запрос возвращает ошибку
// ports.ErrSessionRequired, поэтому прикладной слой отклоняет чтение, а не
// выдаёт выдуманную выжимку.
type Unavailable struct{}

// Unavailable реализует порт чтения.
var _ ports.CommunityReader = Unavailable{}

// ReadCommunity реализует ports.CommunityReader.
func (Unavailable) ReadCommunity(context.Context, ports.ReadRequest) (ports.ReadResult, error) {
	return ports.ReadResult{}, fmt.Errorf("%w: MTProto-сессия не подключена", ports.ErrSessionRequired)
}
