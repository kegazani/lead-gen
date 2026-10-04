package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// AdapterRegistrationRepository — PostgreSQL-реализация
// ports.AdapterRegistrationRepository.
//
// Снимок регистрации хранится заголовком (идентификатор, имя и версия manifest) и
// подчинённой таблицей объявленных возможностей с колонкой порядка. Save пишет
// снимок целиком: заголовок и возможности заменяются в одной транзакции, поэтому
// удалённые из регистрации возможности не остаются строками.
type AdapterRegistrationRepository struct {
	db    execer
	begin func(context.Context) (pgx.Tx, error)
}

// AdapterRegistrationRepository реализует порт репозитория регистраций.
var _ ports.AdapterRegistrationRepository = (*AdapterRegistrationRepository)(nil)

// NewAdapterRegistrationRepository создаёт репозиторий поверх пула соединений.
// Каждый Save выполняется в собственной транзакции.
func NewAdapterRegistrationRepository(pool *pgxpool.Pool) *AdapterRegistrationRepository {
	if pool == nil {
		panic("postgres: NewAdapterRegistrationRepository с nil-пулом")
	}
	return &AdapterRegistrationRepository{db: pool, begin: pool.Begin}
}

// Save сохраняет регистрацию целиком (upsert по идентификатору). Повторный вызов
// для того же идентификатора заменяет прежнее состояние, не создавая вторую.
func (r *AdapterRegistrationRepository) Save(ctx context.Context, registration *domain.AdapterRegistration) error {
	if registration == nil {
		return errors.New("postgres: сохранение nil-регистрации")
	}
	snapshot := registration.Snapshot()
	return r.withTransaction(ctx, func(q execer) error {
		return saveRegistration(ctx, q, snapshot)
	})
}

// PutRegistrationSnapshot кладёт снимок напрямую, минуя домен. Метод существует
// для contract suite и диагностики: он позволяет поместить в хранилище намеренно
// противоречивый снимок и проверить, что загрузка отличается от «нет сущности».
func (r *AdapterRegistrationRepository) PutRegistrationSnapshot(snapshot domain.AdapterRegistrationSnapshot) error {
	ctx := context.Background()
	return r.withTransaction(ctx, func(q execer) error {
		return saveRegistration(ctx, q, snapshot)
	})
}

// FindByID загружает регистрацию по идентификатору через
// domain.RehydrateAdapterRegistration. Отсутствие даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrAdapterRegistrationNotFound).
func (r *AdapterRegistrationRepository) FindByID(ctx context.Context, id domain.AdapterID) (*domain.AdapterRegistration, error) {
	snapshot, err := loadRegistrationSnapshot(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	return rehydrateRegistration(snapshot)
}

// FindByName загружает регистрацию по стабильному имени адаптера.
func (r *AdapterRegistrationRepository) FindByName(ctx context.Context, name domain.AdapterName) (*domain.AdapterRegistration, error) {
	var id domain.AdapterID
	err := r.db.QueryRow(ctx, selectRegistrationIDByName, name.String()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: адаптер %q", ports.ErrAdapterRegistrationNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение регистрации по имени %q: %w", name, err)
	}
	snapshot, err := loadRegistrationSnapshot(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	return rehydrateRegistration(snapshot)
}

// List возвращает регистрации, упорядоченные по идентификатору, — порядок
// детерминирован, чтобы выбор адаптера был воспроизводим.
func (r *AdapterRegistrationRepository) List(ctx context.Context) ([]*domain.AdapterRegistration, error) {
	rows, err := r.db.Query(ctx, selectRegistrationIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение регистраций: %w", err)
	}
	defer rows.Close()

	ids := make([]domain.AdapterID, 0)
	for rows.Next() {
		var id domain.AdapterID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: разбор идентификатора регистрации: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение регистраций: %w", err)
	}

	registrations := make([]*domain.AdapterRegistration, 0, len(ids))
	for _, id := range ids {
		snapshot, err := loadRegistrationSnapshot(ctx, r.db, id)
		if err != nil {
			return nil, err
		}
		registration, err := rehydrateRegistration(snapshot)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

// withTransaction выполняет fn в транзакции, если репозиторий привязан к пулу.
func (r *AdapterRegistrationRepository) withTransaction(ctx context.Context, fn func(execer) error) error {
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

// saveRegistration записывает снимок регистрации в рамках переданного
// исполнителя. Возможности предварительно очищаются: снимок содержит регистрацию
// целиком, поэтому полная замена не оставляет строк, удалённых из manifest.
func saveRegistration(ctx context.Context, q execer, snapshot domain.AdapterRegistrationSnapshot) error {
	manifest := snapshot.Manifest
	if _, err := q.Exec(ctx, upsertRegistration,
		snapshot.ID.String(),
		manifest.AdapterName().String(),
		manifest.Version().String(),
	); err != nil {
		return fmt.Errorf("postgres: запись регистрации %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, deleteCapabilities, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка возможностей регистрации %s: %w", snapshot.ID, err)
	}
	for position, capability := range manifest.Capabilities() {
		if _, err := q.Exec(ctx, insertCapability, snapshot.ID.String(), position, string(capability)); err != nil {
			return fmt.Errorf("postgres: запись возможности %q регистрации %s: %w", capability, snapshot.ID, err)
		}
	}
	return nil
}

// loadRegistrationSnapshot читает снимок регистрации. Отсутствие даёт
// ports.ErrAdapterRegistrationNotFound; неполный manifest отвергается доменной
// ошибкой ErrInvalidSnapshot, а не выдаётся за «нет сущности».
func loadRegistrationSnapshot(ctx context.Context, q execer, id domain.AdapterID) (domain.AdapterRegistrationSnapshot, error) {
	snapshot := domain.AdapterRegistrationSnapshot{}
	var name, version string
	err := q.QueryRow(ctx, selectRegistration, id.String()).Scan(&snapshot.ID, &name, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdapterRegistrationSnapshot{}, fmt.Errorf("%w: %s", ports.ErrAdapterRegistrationNotFound, id)
	}
	if err != nil {
		return domain.AdapterRegistrationSnapshot{}, fmt.Errorf("postgres: чтение регистрации %s: %w", id, err)
	}

	capabilities, err := queryCapabilities(ctx, q, id)
	if err != nil {
		return domain.AdapterRegistrationSnapshot{}, err
	}

	manifest, err := buildManifest(name, version, capabilities)
	if err != nil {
		return domain.AdapterRegistrationSnapshot{}, fmt.Errorf("%w: регистрация %s: %v", domain.ErrInvalidSnapshot, id, err)
	}
	if manifest != nil {
		snapshot.Manifest = *manifest
	}
	return snapshot, nil
}

// queryCapabilities читает объявленные возможности в порядке position.
func queryCapabilities(ctx context.Context, q execer, id domain.AdapterID) ([]domain.CapabilityName, error) {
	rows, err := q.Query(ctx, selectCapabilities, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение возможностей регистрации %s: %w", id, err)
	}
	defer rows.Close()

	capabilities := make([]domain.CapabilityName, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: разбор возможности регистрации %s: %w", id, err)
		}
		capabilities = append(capabilities, domain.CapabilityName(name))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение возможностей регистрации %s: %w", id, err)
	}
	return capabilities, nil
}

// buildManifest собирает manifest из сохранённых частей. Полностью пустые части
// дают нулевой manifest (nil, nil): восстановление отвергнет его доменной
// ошибкой ErrInvalidSnapshot. Неполные или недопустимые части — ошибка.
func buildManifest(name, version string, capabilities []domain.CapabilityName) (*domain.Manifest, error) {
	if name == "" && version == "" && len(capabilities) == 0 {
		return nil, nil
	}
	manifest, err := domain.NewManifest(domain.AdapterName(name), domain.ManifestVersion(version), capabilities)
	if err != nil {
		return nil, err
	}
	return &manifest, nil
}

// rehydrateRegistration восстанавливает агрегат из снимка, проверяя инварианты.
func rehydrateRegistration(snapshot domain.AdapterRegistrationSnapshot) (*domain.AdapterRegistration, error) {
	registration, err := domain.RehydrateAdapterRegistration(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление регистрации %s: %w", snapshot.ID, err)
	}
	return registration, nil
}

// SQL-запросы адаптера регистраций. Схема указана литералом: адаптер работает
// только в схеме сервиса (Schema) и не параметризует её извне.
const (
	upsertRegistration = `
INSERT INTO "integration-platform".adapter_registration (id, adapter_name, manifest_version)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    adapter_name     = EXCLUDED.adapter_name,
    manifest_version = EXCLUDED.manifest_version`

	deleteCapabilities = `DELETE FROM "integration-platform".adapter_capability WHERE adapter_id = $1`

	insertCapability = `
INSERT INTO "integration-platform".adapter_capability (adapter_id, position, name)
VALUES ($1, $2, $3)`

	selectRegistration = `
SELECT id, adapter_name, manifest_version
FROM "integration-platform".adapter_registration
WHERE id = $1`

	selectRegistrationIDByName = `
SELECT id
FROM "integration-platform".adapter_registration
WHERE adapter_name = $1
ORDER BY id
LIMIT 1`

	selectRegistrationIDs = `
SELECT id
FROM "integration-platform".adapter_registration
ORDER BY id`

	selectCapabilities = `
SELECT name
FROM "integration-platform".adapter_capability
WHERE adapter_id = $1
ORDER BY position`
)
