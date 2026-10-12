// Package postgres реализует порты контекста platform-governance поверх
// PostgreSQL: репозиторий тенантов, transactional outbox и транзакцию
// unit-of-work.
//
// Адаптер хранит агрегат не указателем, а снимком (domain.TenantSnapshot): Save
// пишет снимок, FindByID читает его и восстанавливает агрегат через
// domain.RehydrateTenant. Поэтому сохранённое состояние изолировано от
// вызывающего, а загрузка заново проверяет инварианты.
//
// Граница данных — schema-per-service: адаптер работает только в схеме
// platform-governance (Schema) и не обращается к схемам других сервисов.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "platform-governance"

// execer — минимальная поверхность драйвера, нужная адаптерам. Ей удовлетворяют
// и пул соединений, и активная транзакция, поэтому один и тот же код работает и
// вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

// Repository — PostgreSQL-реализация ports.TenantRepository.
type Repository struct {
	db    execer
	begin func(context.Context) (pgx.Tx, error)
}

// Repository реализует порт репозитория тенантов и read-модель списка тенантов.
var (
	_ ports.TenantRepository = (*Repository)(nil)
	_ ports.TenantDirectory  = (*Repository)(nil)
)

// New создаёт репозиторий поверх пула соединений. Каждый Save выполняется в
// собственной транзакции.
func New(pool *pgxpool.Pool) *Repository {
	if pool == nil {
		panic("postgres: New с nil-пулом")
	}
	return &Repository{db: pool, begin: pool.Begin}
}

// NewTx создаёт репозиторий, привязанный к активной транзакции. Запись идёт в
// рамках этой транзакции: commit и rollback остаются за unit-of-work.
// Транзакций нижнего уровня не открывается.
func NewTx(tx pgx.Tx) *Repository {
	if tx == nil {
		panic("postgres: NewTx с nil-транзакцией")
	}
	return &Repository{db: tx}
}

// Save сохраняет агрегат целиком как снимок. Повторный вызов для того же тенанта
// заменяет прежнее состояние (upsert по идентификатору), не создавая второй
// тенант. Составная запись (заголовок, пользователи, аудит) атомарна.
func (r *Repository) Save(ctx context.Context, tenant *domain.Tenant) error {
	if tenant == nil {
		return errors.New("postgres: сохранение nil-тенанта")
	}
	snapshot := tenant.Snapshot()
	return r.withTransaction(ctx, func(q execer) error {
		return saveSnapshot(ctx, q, snapshot)
	})
}

// List возвращает обзор тенантов для админ-консоли: идентификатор, название
// компании и состояние — без пользователей и аудита. Порядок устойчив
// (company_name, затем id), поэтому список экрана воспроизводим. Отдельный
// индекс не заводится: список читает всю таблицу тенантов, а её размер
// определяется числом клиентов платформы, а не объёмом данных.
func (r *Repository) List(ctx context.Context) ([]ports.TenantSummary, error) {
	rows, err := r.db.Query(ctx, selectTenantList)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение списка тенантов: %w", err)
	}
	defer rows.Close()

	summaries := make([]ports.TenantSummary, 0)
	for rows.Next() {
		var (
			id, companyName, state         string
			description, inn, companyEmail string
			website, legalData             string
			profileStatus                  string
			createdAt                      time.Time
		)
		if err := rows.Scan(&id, &companyName, &state, &description, &inn, &companyEmail,
			&website, &legalData, &profileStatus, &createdAt); err != nil {
			return nil, fmt.Errorf("postgres: разбор строки списка тенантов: %w", err)
		}
		summaries = append(summaries, ports.TenantSummary{
			ID:            domain.TenantID(id),
			CompanyName:   companyName,
			State:         domain.TenantState(state),
			Description:   description,
			INN:           inn,
			CompanyEmail:  companyEmail,
			Website:       website,
			LegalData:     legalData,
			ProfileStatus: domain.ProfileStatus(profileStatus),
			CreatedAt:     createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение списка тенантов: %w", err)
	}
	return summaries, nil
}

// FindByID загружает тенант по идентификатору через domain.RehydrateTenant.
// Отсутствие тенанта даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrTenantNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.TenantID) (*domain.Tenant, error) {
	snapshot, err := loadSnapshot(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	tenant, err := domain.RehydrateTenant(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление тенанта %s: %w", id, err)
	}
	return tenant, nil
}

// FindByIdentity ищет тенант по идентифицирующим данным компании — ИНН или
// email компании. Пустые значения не совпадают: идентификация задаётся только
// непустым значением. Отсутствие даёт ports.ErrTenantNotFound.
func (r *Repository) FindByIdentity(ctx context.Context, inn, companyEmail string) (*domain.Tenant, error) {
	var id string
	err := r.db.QueryRow(ctx, findTenantByIdentity, inn, companyEmail).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: inn=%s email=%s", ports.ErrTenantNotFound, inn, companyEmail)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: поиск тенанта по идентифицирующим данным: %w", err)
	}
	return r.FindByID(ctx, domain.TenantID(id))
}

// FindUserByLogin ищет пользователя по логину (email учётной записи) среди всех
// тенантов и возвращает тенант-владельца и идентификатор пользователя. Пустой
// логин не совпадает. Отсутствие даёт ports.ErrTenantNotFound.
func (r *Repository) FindUserByLogin(ctx context.Context, login string) (*domain.Tenant, domain.UserID, error) {
	if login == "" {
		return nil, "", fmt.Errorf("%w: пустой логин", ports.ErrTenantNotFound)
	}
	var tenantID, userID string
	err := r.db.QueryRow(ctx, findUserByLogin, login).Scan(&tenantID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", fmt.Errorf("%w: login=%s", ports.ErrTenantNotFound, login)
	}
	if err != nil {
		return nil, "", fmt.Errorf("postgres: поиск пользователя по логину: %w", err)
	}
	tenant, err := r.FindByID(ctx, domain.TenantID(tenantID))
	if err != nil {
		return nil, "", err
	}
	return tenant, domain.UserID(userID), nil
}

// withTransaction выполняет fn в транзакции, если репозиторий привязан к пулу;
// привязанный к транзакции репозиторий выполняет fn в ней же.
func (r *Repository) withTransaction(ctx context.Context, fn func(execer) error) error {
	if r.begin == nil {
		return fn(r.db)
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// saveSnapshot записывает снимок агрегата в рамках переданного исполнителя.
// Подчинённые таблицы (пользователи, аудит) предварительно очищаются: снимок
// содержит агрегат целиком, поэтому полная замена не оставляет удалённых строк.
func saveSnapshot(ctx context.Context, q execer, snapshot domain.TenantSnapshot) error {
	profileJSON, err := json.Marshal(snapshot.Profile)
	if err != nil {
		return fmt.Errorf("postgres: сериализация профиля тенанта %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, upsertTenant,
		snapshot.ID.String(),
		snapshot.CompanyName,
		snapshot.Description,
		snapshot.INN,
		snapshot.CompanyEmail,
		snapshot.Website,
		snapshot.LegalData,
		snapshot.RegistrationReason,
		profileJSON,
		string(snapshot.ProfileStatus),
		snapshot.CreatedAt,
		string(snapshot.State),
		int64(snapshot.Version),
	); err != nil {
		return fmt.Errorf("postgres: запись тенанта %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, deleteUsers, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка пользователей тенанта %s: %w", snapshot.ID, err)
	}
	for position, user := range snapshot.Users {
		if _, err := q.Exec(ctx, insertUser,
			snapshot.ID.String(),
			position,
			user.ID.String(),
			user.Login,
			user.PasswordHash,
			roleStrings(user.Roles),
		); err != nil {
			return fmt.Errorf("postgres: запись пользователя %s: %w", user.ID, err)
		}
	}
	if _, err := q.Exec(ctx, deleteAudit, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка аудита тенанта %s: %w", snapshot.ID, err)
	}
	for position, record := range snapshot.Audit {
		if _, err := q.Exec(ctx, insertAudit,
			snapshot.ID.String(),
			position,
			record.ID.String(),
			record.Actor.String(),
			string(record.Action),
			record.Object,
			record.At,
			string(record.Outcome),
			record.CorrectionOf.String(),
		); err != nil {
			return fmt.Errorf("postgres: запись аудита %s: %w", record.ID, err)
		}
	}
	return nil
}

// loadSnapshot читает снимок агрегата. Отсутствие тенанта даёт
// ports.ErrTenantNotFound.
func loadSnapshot(ctx context.Context, q execer, id domain.TenantID) (domain.TenantSnapshot, error) {
	snapshot := domain.TenantSnapshot{}
	var state string
	var profileStatus string
	var profileRaw []byte
	var version int64
	err := q.QueryRow(ctx, selectTenant, id.String()).Scan(
		&snapshot.ID,
		&snapshot.CompanyName,
		&snapshot.Description,
		&snapshot.INN,
		&snapshot.CompanyEmail,
		&snapshot.Website,
		&snapshot.LegalData,
		&snapshot.RegistrationReason,
		&profileRaw,
		&profileStatus,
		&snapshot.CreatedAt,
		&state,
		&version,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TenantSnapshot{}, fmt.Errorf("%w: %s", ports.ErrTenantNotFound, id)
	}
	if err != nil {
		return domain.TenantSnapshot{}, fmt.Errorf("postgres: чтение тенанта %s: %w", id, err)
	}
	snapshot.State = domain.TenantState(state)
	snapshot.ProfileStatus = domain.ProfileStatus(profileStatus)
	if len(profileRaw) > 0 {
		if err := json.Unmarshal(profileRaw, &snapshot.Profile); err != nil {
			return domain.TenantSnapshot{}, fmt.Errorf("postgres: разбор профиля тенанта %s: %w", id, err)
		}
	}
	if version > 0 {
		snapshot.Version = uint64(version)
	}

	users, err := queryUsers(ctx, q, id)
	if err != nil {
		return domain.TenantSnapshot{}, err
	}
	snapshot.Users = users

	audit, err := queryAudit(ctx, q, id)
	if err != nil {
		return domain.TenantSnapshot{}, err
	}
	snapshot.Audit = audit
	return snapshot, nil
}

// queryUsers читает состав тенанта в порядке добавления.
func queryUsers(ctx context.Context, q execer, id domain.TenantID) ([]domain.TenantUserSnapshot, error) {
	rows, err := q.Query(ctx, selectUsers, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение пользователей тенанта %s: %w", id, err)
	}
	defer rows.Close()

	users := make([]domain.TenantUserSnapshot, 0)
	for rows.Next() {
		var userID, login, passwordHash string
		var roles []string
		if err := rows.Scan(&userID, &login, &passwordHash, &roles); err != nil {
			return nil, fmt.Errorf("postgres: разбор пользователя тенанта %s: %w", id, err)
		}
		users = append(users, domain.TenantUserSnapshot{
			ID:           domain.UserID(userID),
			Login:        login,
			PasswordHash: passwordHash,
			Roles:        roleValues(roles),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение пользователей тенанта %s: %w", id, err)
	}
	return users, nil
}

// queryAudit читает журнал аудита тенанта в порядке добавления.
func queryAudit(ctx context.Context, q execer, id domain.TenantID) ([]domain.AuditRecordSnapshot, error) {
	rows, err := q.Query(ctx, selectAudit, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение аудита тенанта %s: %w", id, err)
	}
	defer rows.Close()

	records := make([]domain.AuditRecordSnapshot, 0)
	for rows.Next() {
		var record domain.AuditRecordSnapshot
		var actor, action, outcome, correction string
		if err := rows.Scan(&record.ID, &actor, &action, &record.Object, &record.At, &outcome, &correction); err != nil {
			return nil, fmt.Errorf("postgres: разбор записи аудита тенанта %s: %w", id, err)
		}
		record.Actor = domain.ActorID(actor)
		record.Action = domain.AuditAction(action)
		record.Outcome = domain.AuditOutcome(outcome)
		record.CorrectionOf = domain.AuditRecordID(correction)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение аудита тенанта %s: %w", id, err)
	}
	return records, nil
}

// roleStrings переводит роли домена в массив строк для драйвера.
func roleStrings(roles []domain.Role) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, role.String())
	}
	return out
}

// roleValues переводит массив строк драйвера в роли домена.
func roleValues(roles []string) []domain.Role {
	out := make([]domain.Role, 0, len(roles))
	for _, role := range roles {
		out = append(out, domain.Role(role))
	}
	return out
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в
// схеме сервиса (Schema) и не параметризует её извне.
const (
	upsertTenant = `
INSERT INTO "platform-governance".tenant (id, company_name, description, inn, company_email, website, legal_data, registration_reason, profile, profile_status, created_at, state, version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO UPDATE SET
    company_name        = EXCLUDED.company_name,
    description         = EXCLUDED.description,
    inn                 = EXCLUDED.inn,
    company_email       = EXCLUDED.company_email,
    website             = EXCLUDED.website,
    legal_data          = EXCLUDED.legal_data,
    registration_reason = EXCLUDED.registration_reason,
    profile             = EXCLUDED.profile,
    profile_status      = EXCLUDED.profile_status,
    created_at          = EXCLUDED.created_at,
    state               = EXCLUDED.state,
    version             = EXCLUDED.version`

	deleteUsers = `DELETE FROM "platform-governance".tenant_user WHERE tenant_id = $1`

	insertUser = `
INSERT INTO "platform-governance".tenant_user (tenant_id, position, id, login, password_hash, roles)
VALUES ($1, $2, $3, $4, $5, $6)`

	deleteAudit = `DELETE FROM "platform-governance".audit_record WHERE tenant_id = $1`

	insertAudit = `
INSERT INTO "platform-governance".audit_record (tenant_id, position, id, actor, action, object, at, outcome, correction_of)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	selectTenant = `
SELECT id, company_name, description, inn, company_email, website, legal_data, registration_reason, profile, profile_status, created_at, state, version
FROM "platform-governance".tenant
WHERE id = $1`

	selectTenantList = `
SELECT id, company_name, state, description, inn, company_email, website, legal_data, profile_status, created_at
FROM "platform-governance".tenant
ORDER BY company_name, id`

	findTenantByIdentity = `
SELECT id
FROM "platform-governance".tenant
WHERE (inn <> '' AND inn = $1)
   OR (company_email <> '' AND company_email = $2)
LIMIT 1`

	findUserByLogin = `
SELECT tenant_id, id
FROM "platform-governance".tenant_user
WHERE login = $1
LIMIT 1`

	selectUsers = `
SELECT id, login, password_hash, roles
FROM "platform-governance".tenant_user
WHERE tenant_id = $1
ORDER BY position`

	selectAudit = `
SELECT id, actor, action, object, at, outcome, correction_of
FROM "platform-governance".audit_record
WHERE tenant_id = $1
ORDER BY position`
)

// jsonPayload сериализует полезную нагрузку сообщения outbox в jsonb.
func jsonPayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte("null"), nil
	}
	return json.Marshal(payload)
}
