package domain

import (
	"fmt"
	"strings"
)

// Имена каналов, объявленных доменом. Канал определяет, каким способом
// разрешено выполнить утверждённое действие.
const (
	ActionChannelEmailName = "email"
	ActionChannelCallName  = "call"
	ActionChannelFormName  = "form"
)

var allowedActionChannels = map[string]struct{}{
	ActionChannelEmailName: {},
	ActionChannelCallName:  {},
	ActionChannelFormName:  {},
}

// ActionChannel — value object: канал, по которому разрешено выполнить
// утверждённое действие. Неизменяем; создать значение можно только через
// ParseActionChannel, поэтому неизвестный канал в домен не попадает.
type ActionChannel struct {
	name string
}

// ParseActionChannel разбирает имя канала. Регистр и окружающие пробелы не
// значимы; неизвестное имя отвергается.
func ParseActionChannel(name string) (ActionChannel, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if _, ok := allowedActionChannels[n]; !ok {
		return ActionChannel{}, fmt.Errorf("%w: %q", ErrUnknownActionChannel, name)
	}
	return ActionChannel{name: n}, nil
}

// String возвращает каноническое имя канала.
func (c ActionChannel) String() string { return c.name }

// IsZero сообщает, что канал не задан.
func (c ActionChannel) IsZero() bool { return c.name == "" }

// Equals сравнивает каналы.
func (c ActionChannel) Equals(other ActionChannel) bool { return c.name == other.name }
