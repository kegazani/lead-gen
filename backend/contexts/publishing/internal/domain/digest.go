package domain

import (
	"fmt"
	"strings"
)

// DigestRef — ссылка на выжимку-источник публикации. Публикация строится из
// готовой выжимки сообщества Telegram; доступа к сырым сообщениям и данным
// авторов у контекста нет (design.md, Решение 3).
type DigestRef struct {
	// ID — идентификатор выжимки.
	ID string
	// CommunityID — сообщество, по которому построена выжимка.
	CommunityID string
	// Period — период сообщений, вошедших в выжимку.
	Period string
}

// Validate проверяет обязательные поля ссылки.
func (r DigestRef) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%w: digest_id", ErrDigestRequired)
	}
	if strings.TrimSpace(r.CommunityID) == "" {
		return fmt.Errorf("%w: community_id", ErrDigestRequired)
	}
	if strings.TrimSpace(r.Period) == "" {
		return fmt.Errorf("%w: period", ErrDigestRequired)
	}
	return nil
}

// Digest — прочитанная выжимка: её темы и частоты. Сырых сообщений и авторов
// здесь нет по построению.
type Digest struct {
	Ref    DigestRef
	Topics []string
}
