package domain

import (
	"fmt"
	"strings"
)

// Platform — площадка публикации. Набор закрыт: неизвестная площадка в домен не
// проходит. Instagram/Meta в набор не входит: канал выведен из объёма по
// правовому основанию, а не по способу доступа.
type Platform string

const (
	// PlatformDzen — Дзен (статья).
	PlatformDzen Platform = "dzen"
	// PlatformLinkedIn — LinkedIn (пост).
	PlatformLinkedIn Platform = "linkedin"
	// PlatformTelegram — сообщество Telegram (сообщение).
	PlatformTelegram Platform = "telegram"
)

// format — формат материала, ожидаемый площадкой. Kind различает виды
// публикаций, MaxBody — предельная длина текста в рунах.
type format struct {
	Kind    string
	MaxBody int
}

var platformFormats = map[Platform]format{
	PlatformDzen:     {Kind: "article", MaxBody: 4000},
	PlatformLinkedIn: {Kind: "post", MaxBody: 3000},
	PlatformTelegram: {Kind: "message", MaxBody: 4096},
}

// Дзен получает статью, LinkedIn — пост, Telegram — сообщение: один материал на
// три площадки не формируется (design.md, Решение 4).

// ParsePlatform разбирает имя площадки. Регистр и окружающие пробелы не значимы;
// неизвестное имя отвергается. Возвращается каноническое имя.
func ParsePlatform(name string) (Platform, error) {
	normalized := Platform(strings.ToLower(strings.TrimSpace(name)))
	if _, ok := platformFormats[normalized]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownPlatform, name)
	}
	return normalized, nil
}

// Valid сообщает, объявлена ли площадка доменом.
func (p Platform) Valid() bool {
	_, ok := platformFormats[p]
	return ok
}

// Kind возвращает вид публикации площадки (article, post, message).
func (p Platform) Kind() string { return platformFormats[p].Kind }

// MaxBody возвращает предельную длину текста площадки в рунах. Для неизвестной
// площадки возвращается ноль: вызывающий обязан был отсеять её раньше.
func (p Platform) MaxBody() int { return platformFormats[p].MaxBody }

func (p Platform) String() string { return string(p) }

// PublicationTarget — value object «площадка и учётная запись назначения
// публикации». Учётные записи принадлежат платформе, а не клиентам: набор
// реквизитов на канал один.
type PublicationTarget struct {
	platform Platform
	account  string
}

// PublicationTargetSnapshot — сохранённое состояние value object target.
type PublicationTargetSnapshot struct {
	Platform Platform
	Account  string
}

// NewPublicationTarget собирает и проверяет цель публикации. Площадка обязана
// быть объявлена доменом, учётная запись — непустой.
func NewPublicationTarget(platform, account string) (PublicationTarget, error) {
	parsed, err := ParsePlatform(platform)
	if err != nil {
		return PublicationTarget{}, err
	}
	trimmed := strings.TrimSpace(account)
	if trimmed == "" {
		return PublicationTarget{}, fmt.Errorf("%w: %s", ErrEmptyAccount, parsed)
	}
	return PublicationTarget{platform: parsed, account: trimmed}, nil
}

// Platform возвращает площадку назначения.
func (t PublicationTarget) Platform() Platform { return t.platform }

// Account возвращает учётную запись назначения.
func (t PublicationTarget) Account() string { return t.account }

// String возвращает читаемое представление цели.
func (t PublicationTarget) String() string {
	return "platform=" + string(t.platform) + ",account=" + t.account
}

// Snapshot возвращает состояние value object для сохранения.
func (t PublicationTarget) Snapshot() PublicationTargetSnapshot {
	return PublicationTargetSnapshot{Platform: t.platform, Account: t.account}
}

// rehydrateTarget восстанавливает и перепроверяет цель из снимка.
func rehydrateTarget(s PublicationTargetSnapshot) (PublicationTarget, error) {
	return NewPublicationTarget(string(s.Platform), s.Account)
}
