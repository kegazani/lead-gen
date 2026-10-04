package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// Links — PostgreSQL-реализация ports.TransitionLinks: реестр трекинг-ссылок
// capability read-tracking. По непрозрачной ссылке владелец отдаёт адрес
// назначения перехода.
type Links struct {
	db execer
}

// Links реализует порт реестра трекинг-ссылок.
var _ ports.TransitionLinks = (*Links)(nil)

// NewLinks создаёт реестр поверх пула соединений.
func NewLinks(pool *pgxpool.Pool) *Links {
	if pool == nil {
		panic("postgres: NewLinks с nil-пулом")
	}
	return &Links{db: pool}
}

// newLinksTx создаёт реестр, привязанный к активной транзакции.
func newLinksTx(tx execer) *Links {
	return &Links{db: tx}
}

// Save регистрирует ссылку перехода. Повторная регистрация той же ссылки
// заменяет прежний адрес и владельца: владелец ссылки вправе её переопределить.
func (l *Links) Save(ctx context.Context, link domain.TransitionLink) error {
	if strings.TrimSpace(string(link.Ref)) == "" {
		return errors.New("postgres: регистрация ссылки без идентификатора")
	}
	if _, err := l.db.Exec(ctx, upsertLink,
		link.Ref.String(),
		link.Target,
		link.TenantID.String(),
		link.CompanyID.String(),
	); err != nil {
		return fmt.Errorf("postgres: регистрация ссылки %s: %w", link.Ref, err)
	}
	return nil
}

// Resolve возвращает ссылку перехода по непрозрачному значению. Отсутствие даёт
// ошибку, для которой истинно errors.Is(err, ports.ErrLinkNotFound).
func (l *Links) Resolve(ctx context.Context, ref domain.LinkRef) (domain.TransitionLink, error) {
	var (
		rawRef    string
		target    string
		tenantID  string
		companyID string
	)
	err := l.db.QueryRow(ctx, selectLink, ref.String()).Scan(&rawRef, &target, &tenantID, &companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TransitionLink{}, fmt.Errorf("%w: %s", ports.ErrLinkNotFound, ref)
	}
	if err != nil {
		return domain.TransitionLink{}, fmt.Errorf("postgres: чтение ссылки %s: %w", ref, err)
	}
	return domain.TransitionLink{
		Ref:       domain.LinkRef(rawRef),
		Target:    target,
		TenantID:  domain.TenantID(tenantID),
		CompanyID: domain.CompanyID(companyID),
	}, nil
}

// SQL-запросы реестра трекинг-ссылок.
const (
	upsertLink = `
INSERT INTO "engagement-observation".transition_link (ref, target, tenant_id, company_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (ref) DO UPDATE SET
    target     = EXCLUDED.target,
    tenant_id  = EXCLUDED.tenant_id,
    company_id = EXCLUDED.company_id`

	selectLink = `
SELECT ref, target, tenant_id, company_id
FROM "engagement-observation".transition_link
WHERE ref = $1`
)
