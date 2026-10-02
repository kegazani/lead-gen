package domain

import (
	"fmt"
	"strings"
)

// SourceKind — вид источника чтения сообщества.
type SourceKind string

const (
	// SourceUserAccount — MTProto под пользовательским аккаунтом с
	// авторизованной сессией. Единственный допустимый вид источника.
	SourceUserAccount SourceKind = "user-account"

	// SourceBot — Bot API. Отклоняется: не даёт ни истории, ни состава
	// участников (spec, «Чтение через авторизованную сессию»).
	SourceBot SourceKind = "bot"
)

// isValid сообщает, объявлен ли вид источника доменом.
func (k SourceKind) isValid() bool {
	return k == SourceUserAccount || k == SourceBot
}

// CommunitySource — value object «разрешённый источник сообщений сообщества»
// (architecture/domain-catalog.yaml, value_objects: community-source).
//
// Поля закрыты: источник создаётся только через NewCommunitySource, который
// применяет policy authorized-community-read. Идентификатор сообщества и его
// публичное название — метаданные сообщества, а не данные участников: авторов
// здесь нет по построению.
type CommunitySource struct {
	id         CommunityID
	title      string
	kind       SourceKind
	authorized bool
}

// NewCommunitySourceParams — данные источника.
type NewCommunitySourceParams struct {
	// ID — идентификатор сообщества (непустой).
	ID CommunityID
	// Title — публичное название сообщества (непустое).
	Title string
	// Kind — вид источника. Допустим только SourceUserAccount.
	Kind SourceKind
	// Authorized — сообщество разрешено к чтению. Ложь отклоняется.
	Authorized bool
}

// NewCommunitySource создаёт источник, применяя policy authorized-community-read:
// непустые идентификатор и название, допустимый вид и обязательное разрешение на
// чтение. Бот-источник отвергается отдельной ошибкой.
func NewCommunitySource(p NewCommunitySourceParams) (CommunitySource, error) {
	if err := p.ID.validate(); err != nil {
		return CommunitySource{}, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return CommunitySource{}, ErrEmptySourceTitle
	}
	if !p.Kind.isValid() {
		return CommunitySource{}, fmt.Errorf("%w: %q", ErrUnknownSourceKind, p.Kind)
	}
	if p.Kind == SourceBot {
		return CommunitySource{}, ErrBotSourceRejected
	}
	if !p.Authorized {
		return CommunitySource{}, ErrSourceNotAuthorized
	}
	return CommunitySource{
		id:         p.ID,
		title:      strings.TrimSpace(p.Title),
		kind:       p.Kind,
		authorized: true,
	}, nil
}

// ID возвращает идентификатор сообщества.
func (s CommunitySource) ID() CommunityID { return s.id }

// Title возвращает публичное название сообщества.
func (s CommunitySource) Title() string { return s.title }

// Kind возвращает вид источника.
func (s CommunitySource) Kind() SourceKind { return s.kind }

// IsAuthorized сообщает, разрешено ли чтение источника. Для успешно созданного
// источника всегда истинно, но метод нужен вызывающим для явной проверки.
func (s CommunitySource) IsAuthorized() bool { return s.authorized }

// Snapshot возвращает состояние источника для сохранения.
func (s CommunitySource) Snapshot() CommunitySourceSnapshot {
	return CommunitySourceSnapshot{
		ID:         s.id,
		Title:      s.title,
		Kind:       s.kind,
		Authorized: s.authorized,
	}
}

// CommunitySourceSnapshot — сохранённое состояние источника.
type CommunitySourceSnapshot struct {
	ID         CommunityID
	Title      string
	Kind       SourceKind
	Authorized bool
}

// RehydrateCommunitySource восстанавливает источник из снимка, повторно проверяя
// инварианты: сохранённый неавторизованный или бот-источник недопустим.
func RehydrateCommunitySource(s CommunitySourceSnapshot) (CommunitySource, error) {
	source, err := NewCommunitySource(NewCommunitySourceParams{
		ID:         s.ID,
		Title:      s.Title,
		Kind:       s.Kind,
		Authorized: s.Authorized,
	})
	if err != nil {
		return CommunitySource{}, fmt.Errorf("%w: источник %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	return source, nil
}
