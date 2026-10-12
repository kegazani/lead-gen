package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RoleSet описывает три независимые identity одной схемы. Пароли передаются
// вызывающей стороной из secret files и нигде не сериализуются или логируются.
type RoleSet struct {
	Schema             Schema
	OwnerRole          string
	MigratorRole       string
	MigratorPassword   string
	RuntimeRole        string
	RuntimePassword    string
	RuntimeScope       *RuntimeRoleScope
	OwnedRelations     []string
	OwnedRoutines      []RoutineRef
	OwnedTypes         []string
	OwnedCollations    []string
	Grants             []TableGrant
	SequenceGrants     []SequenceGrant
	AdditionalRuntimes []RuntimeIdentity
}

// RuntimeRoleScope — доверенный deployment scope для candidate runtime identity.
// Эти значения не включаются в PostgreSQL identifier: имя роли содержит только
// криптографический digest scope.
type RuntimeRoleScope struct {
	Environment string
	Deployment  string
	Target      string
	Generation  string
}

// RuntimeIdentity представляет отдельный deploy/runtime role на одну managed
// schema; grants разных приложений не объединяются в одну shared credential.
type RuntimeIdentity struct {
	Role           string
	Password       string
	RuntimeScope   *RuntimeRoleScope
	Grants         []TableGrant
	SequenceGrants []SequenceGrant
}

// RoutineRef идентифицирует routine по kind и identity arguments PostgreSQL.
type RoutineRef struct {
	Kind string // function, procedure или aggregate; window functions считаются function.
	Name string
	Args string
}

// TableGrant задаёт минимальные runtime-права на таблицу в собственной схеме.
// Только перечисленные операции выдаются; TRUNCATE и DDL отсутствуют в модели.
type TableGrant struct {
	Table      string
	Privileges []string
}

// SequenceGrant задаёт точные права на sequence, включая GENERATED/serial ids.
type SequenceGrant struct {
	Sequence   string
	Privileges []string
}

// RequestIntakeRoleSets задаёт production grant matrix для первого пилота.
// Пароли поступают из раздельных secret files для миграционной и runtime-ролей.
func RequestIntakeRoleSets(requestMigratorPassword, requestRuntimePassword, consoleMigratorPassword, consoleRuntimePassword, vectorMigratorPassword, vectorRuntimePassword, integrationRuntimePassword string) []RoleSet {
	return []RoleSet{
		{
			Schema: Schema("request-fulfillment"), OwnerRole: "leadgen_request_fulfillment_owner",
			MigratorRole: "leadgen_request_fulfillment_migrator", MigratorPassword: requestMigratorPassword,
			RuntimeRole: "leadgen_request_fulfillment_runtime", RuntimePassword: requestRuntimePassword,
			OwnedRelations: []string{"schema_migrations", "request", "email_draft", "outbox", "execution_attempt", "execution_resolution", "execution_resolution_revision"},
			OwnedRoutines: []RoutineRef{
				{Kind: "function", Name: "reject_execution_resolution_mutation"}, {Kind: "function", Name: "validate_execution_resolution_subject"},
				{Kind: "function", Name: "reject_execution_resolution_truncate"}, {Kind: "function", Name: "reject_legacy_execution_resolution_truncate"},
				{Kind: "function", Name: "validate_execution_resolution_revision"},
				{Kind: "function", Name: "reject_execution_resolution_revision_mutation"}, {Kind: "function", Name: "reject_legacy_execution_resolution_insert"},
			},
			Grants: []TableGrant{
				{Table: "request", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
				{Table: "email_draft", Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
				{Table: "outbox", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
				{Table: "execution_attempt", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
				{Table: "execution_resolution", Privileges: []string{"SELECT"}},
				{Table: "execution_resolution_revision", Privileges: []string{"SELECT", "INSERT"}},
				{Table: migrationsTable, Privileges: []string{"SELECT"}},
			},
		},
		{
			Schema: Schema("consoleauth"), OwnerRole: "leadgen_consoleauth_owner",
			MigratorRole: "leadgen_consoleauth_migrator", MigratorPassword: consoleMigratorPassword,
			RuntimeRole: "leadgen_consoleauth_runtime", RuntimePassword: consoleRuntimePassword,
			OwnedRelations: []string{"schema_migrations", "console_sessions"},
			Grants: []TableGrant{
				{Table: "console_sessions", Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
				{Table: migrationsTable, Privileges: []string{"SELECT"}},
			},
		},
		{
			Schema: Schema("integration-platform"), OwnerRole: "leadgen_integration_platform_owner",
			MigratorRole: "leadgen_integration_platform_migrator", MigratorPassword: vectorMigratorPassword,
			RuntimeRole: "leadgen_integration_platform_intake_runtime", RuntimePassword: vectorRuntimePassword,
			OwnedRelations: []string{"schema_migrations", "adapter_registration", "adapter_capability", "integration_invocation", "integration_invocation_seq_seq", "vector_document"},
			Grants: []TableGrant{
				{Table: "vector_document", Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
				{Table: migrationsTable, Privileges: []string{"SELECT"}},
			},
			AdditionalRuntimes: []RuntimeIdentity{{
				Role: "leadgen_integration_platform_runtime", Password: integrationRuntimePassword,
				Grants: []TableGrant{
					{Table: "adapter_registration", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
					{Table: "adapter_capability", Privileges: []string{"SELECT", "INSERT", "DELETE"}},
					{Table: "integration_invocation", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
					{Table: migrationsTable, Privileges: []string{"SELECT"}},
				},
				SequenceGrants: []SequenceGrant{{Sequence: "integration_invocation_seq_seq", Privileges: []string{"USAGE", "SELECT"}}},
			}},
		},
	}
}

// RequestIntakeRoleSetsForGeneration возвращает только роли runtime, выведенные
// из проверенного deployment/generation scope. Имена owner/migrator остаются
// стабильными и управляемыми отдельно.
func RequestIntakeRoleSetsForGeneration(scope RuntimeRoleScope, requestMigratorPassword, requestRuntimePassword, consoleMigratorPassword, consoleRuntimePassword, vectorMigratorPassword, vectorRuntimePassword, integrationRuntimePassword string) ([]RoleSet, error) {
	sets := RequestIntakeRoleSets(requestMigratorPassword, requestRuntimePassword, consoleMigratorPassword, consoleRuntimePassword, vectorMigratorPassword, vectorRuntimePassword, integrationRuntimePassword)
	for i := range sets {
		runtimeScope := scope
		runtimeScope.Target = sets[i].Schema.String()
		role, err := RuntimeRoleName(runtimeScope)
		if err != nil {
			return nil, err
		}
		sets[i].RuntimeRole = role
		sets[i].RuntimeScope = &runtimeScope
		for j := range sets[i].AdditionalRuntimes {
			additionalScope := scope
			additionalScope.Target = "integration-platform-full-runtime"
			additionalRole, err := RuntimeRoleName(additionalScope)
			if err != nil {
				return nil, err
			}
			sets[i].AdditionalRuntimes[j].Role = additionalRole
			sets[i].AdditionalRuntimes[j].RuntimeScope = &additionalScope
		}
	}
	return sets, nil
}

// ProvisionSchema создаёт identities/schema и переносит ownership только
// перечисленных объектов к NOLOGIN owner. Вызывается bootstrap/admin до миграции.
// Runtime table grants выдаются отдельно через GrantRuntimeSchema после ApplyAs.
func ProvisionSchema(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	if pool == nil {
		return errors.New("migrate: provision: пул соединений не задан")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRoleSet(spec); err != nil {
		return err
	}
	if spec.MigratorPassword == "" || spec.RuntimePassword == "" {
		return errors.New("migrate: provision: пароль login-роли не задан")
	}
	for _, runtime := range spec.AdditionalRuntimes {
		if runtime.Password == "" {
			return fmt.Errorf("migrate: provision: пароль дополнительной runtime-роли %q не задан", runtime.Role)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: provision: начало транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var serverVersion int
	if err := tx.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&serverVersion); err != nil {
		return fmt.Errorf("migrate: provision: проверка версии PostgreSQL: %w", err)
	}
	if serverVersion < 160000 {
		return fmt.Errorf("migrate: provision: PostgreSQL %d не поддерживает безопасные SET/INHERIT role memberships; требуется 16+", serverVersion)
	}

	roles := []struct {
		name     string
		login    bool
		password string
	}{{spec.OwnerRole, false, ""}, {spec.MigratorRole, true, spec.MigratorPassword}, {spec.RuntimeRole, true, spec.RuntimePassword}}
	for _, runtime := range spec.AdditionalRuntimes {
		roles = append(roles, struct {
			name     string
			login    bool
			password string
		}{runtime.Role, true, runtime.Password})
	}
	for _, role := range roles {
		if err := ensureRole(ctx, tx, role.name, role.login, role.password); err != nil {
			return fmt.Errorf("migrate: provision: настройка роли %q: %w", role.name, err)
		}
	}

	if err := normalizeRoleMemberships(ctx, tx, spec); err != nil {
		return fmt.Errorf("migrate: provision: нормализация membership managed identities: %w", err)
	}
	if err := clearOwnerDefaultACLs(ctx, tx, spec); err != nil {
		return err
	}
	owner := pgx.Identifier{spec.OwnerRole}.Sanitize()
	// PostgreSQL per-schema default ACLs only add to global defaults; they do
	// not override global PUBLIC EXECUTE. The managed owner is unique to this
	// schema, so clear that owner's global function default without touching
	// PUBLIC database privileges or any other role's defaults.
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: global function default ACL owner %q: %w", spec.OwnerRole, err)
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" REVOKE USAGE ON TYPES FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: global type default ACL owner %q: %w", spec.OwnerRole, err)
	}
	migrator := pgx.Identifier{spec.MigratorRole}.Sanitize()
	schema := pgx.Identifier{spec.Schema.String()}.Sanitize()
	if _, err := tx.Exec(ctx, "GRANT "+owner+" TO "+migrator+" WITH INHERIT FALSE, SET TRUE"); err != nil {
		return fmt.Errorf("migrate: provision: выдача migrator membership для схемы %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema+" AUTHORIZATION "+owner); err != nil {
		return fmt.Errorf("migrate: provision: создание схемы %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+schema+" OWNER TO "+owner); err != nil {
		return fmt.Errorf("migrate: provision: назначение владельца схемы %q: %w", spec.Schema, err)
	}
	if err := transferAllowlistedOwnership(ctx, tx, spec); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "REVOKE ALL ON SCHEMA "+schema+" FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: отзыв публичных прав схемы %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "REVOKE CREATE ON SCHEMA "+schema+" FROM "+pgx.Identifier{spec.RuntimeRole}.Sanitize()); err != nil {
		return fmt.Errorf("migrate: provision: отзыв CREATE runtime-схеме %q: %w", spec.Schema, err)
	}
	for _, runtime := range runtimeIdentities(spec) {
		if _, err := tx.Exec(ctx, "ALTER ROLE "+pgx.Identifier{runtime.Role}.Sanitize()+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
			return fmt.Errorf("migrate: provision: ограничение runtime-роли %q: %w", runtime.Role, err)
		}
	}
	if _, err := tx.Exec(ctx, "ALTER ROLE "+migrator+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
		return fmt.Errorf("migrate: provision: ограничение migration-роли: %w", err)
	}
	if err := normalizeManagedACLs(ctx, tx, spec); err != nil {
		return err
	}
	var database string
	if err := tx.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return fmt.Errorf("migrate: provision: определение базы: %w", err)
	}
	if _, err := tx.Exec(ctx, "REVOKE CREATE, TEMPORARY ON DATABASE "+pgx.Identifier{database}.Sanitize()+" FROM "+pgx.Identifier{spec.RuntimeRole}.Sanitize()); err != nil {
		return fmt.Errorf("migrate: provision: отзыв прав базы у runtime-роли: %w", err)
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE ALL ON TABLES FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: default privileges для таблиц %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE ALL ON SEQUENCES FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: default privileges для sequences %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE USAGE ON TYPES FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: default type privileges %q: %w", spec.Schema, err)
	}
	for _, runtime := range runtimeIdentities(spec) {
		role := pgx.Identifier{runtime.Role}.Sanitize()
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE ALL ON TABLES FROM "+role); err != nil {
			return fmt.Errorf("migrate: provision: default table grants runtime %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE ALL ON SEQUENCES FROM "+role); err != nil {
			return fmt.Errorf("migrate: provision: default sequence grants runtime %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE EXECUTE ON FUNCTIONS FROM "+role); err != nil {
			return fmt.Errorf("migrate: provision: default function grants runtime %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE USAGE ON TYPES FROM "+role); err != nil {
			return fmt.Errorf("migrate: provision: default type grants runtime %q: %w", spec.Schema, err)
		}
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: сброс default function grants %q: %w", spec.Schema, err)
	}
	if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+owner+" IN SCHEMA "+schema+" REVOKE USAGE ON TYPES FROM PUBLIC"); err != nil {
		return fmt.Errorf("migrate: provision: сброс default type grants %q: %w", spec.Schema, err)
	}
	return tx.Commit(ctx)
}

// GrantRuntimeSchema выполняется после миграций владельческой ролью/администратором.
// Перед выдачей policy сначала отзывает у runtime все права на объекты схемы,
// затем выдаёт только перечисленные table privileges; будущие таблицы прав не получают.
func GrantRuntimeSchema(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	if pool == nil {
		return errors.New("migrate: grants: пул соединений не задан")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRoleSet(spec); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: grants: начало транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := grantRuntimeSchemaTx(ctx, tx, spec); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GrantRuntimeSchemaAs выдаёт runtime grants от имени NOLOGIN owner через
// migration login. Как и ApplyAs, роль назначается на одном выделенном
// соединении и гарантированно сбрасывается до возврата соединения в пул.
func GrantRuntimeSchemaAs(ctx context.Context, pool *pgxpool.Pool, spec RoleSet, ownerRole string) (resultErr error) {
	if pool == nil {
		return errors.New("migrate: grants: пул соединений не задан")
	}
	if strings.TrimSpace(ownerRole) == "" || ownerRole != spec.OwnerRole {
		return errors.New("migrate: grants: owner role не соответствует target")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRoleSet(spec); err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: grants: соединение migration pool: %w", err)
	}
	roleApplied := false
	discard := false
	defer func() {
		if roleApplied && !discard {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, cleanupErr := conn.Exec(cleanupCtx, "RESET ROLE")
			cancel()
			if cleanupErr != nil {
				discard = true
				resultErr = errors.Join(resultErr, errors.New("migrate: grants: RESET ROLE не подтверждён"))
			}
		}
		if discard {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = conn.Hijack().Close(cleanupCtx)
			return
		}
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
		discard = true
		return errors.New("migrate: grants: не удалось принять target owner")
	}
	roleApplied = true
	if err := verifyMigrationAuthority(ctx, conn, spec.Schema, ownerRole); err != nil {
		return fmt.Errorf("migrate: grants: authority check отказал: %w", err)
	}
	var schemaOwner bool
	if err := conn.QueryRow(ctx, "SELECT nspowner=(SELECT oid FROM pg_roles WHERE rolname=$2) FROM pg_namespace WHERE nspname=$1", spec.Schema.String(), ownerRole).Scan(&schemaOwner); err != nil || !schemaOwner {
		return errors.New("migrate: grants: target owner не владеет схемой")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return errors.New("migrate: grants: начало транзакции от owner не удалось")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := grantRuntimeSchemaTx(ctx, tx, spec); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("migrate: grants: commit runtime grants не удался")
	}
	return nil
}

func grantRuntimeSchemaTx(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	schema := pgx.Identifier{spec.Schema.String()}.Sanitize()
	if err := normalizeManagedACLs(ctx, tx, spec); err != nil {
		return err
	}
	if err := clearOwnerDefaultACLs(ctx, tx, spec); err != nil {
		return err
	}
	for _, runtime := range runtimeIdentities(spec) {
		role := pgx.Identifier{runtime.Role}.Sanitize()
		if _, err := tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role); err != nil {
			return fmt.Errorf("migrate: grants: USAGE схемы %q runtime %q: %w", spec.Schema, runtime.Role, err)
		}
		if _, err := tx.Exec(ctx, "REVOKE CREATE ON SCHEMA "+schema+" FROM "+role); err != nil {
			return fmt.Errorf("migrate: grants: запрет CREATE схемы %q runtime %q: %w", spec.Schema, runtime.Role, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" IN SCHEMA "+schema+" REVOKE ALL ON TABLES FROM "+role); err != nil {
			return fmt.Errorf("migrate: grants: запрет default table privileges %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" IN SCHEMA "+schema+" REVOKE ALL ON SEQUENCES FROM "+role); err != nil {
			return fmt.Errorf("migrate: grants: запрет default sequence privileges %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" IN SCHEMA "+schema+" REVOKE EXECUTE ON FUNCTIONS FROM "+role); err != nil {
			return fmt.Errorf("migrate: grants: запрет default function privileges %q: %w", spec.Schema, err)
		}
		if _, err := tx.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" IN SCHEMA "+schema+" REVOKE USAGE ON TYPES FROM "+role); err != nil {
			return fmt.Errorf("migrate: grants: запрет default type privileges %q: %w", spec.Schema, err)
		}
		for _, grant := range runtime.Grants {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname=$1 AND relation.relname=$2 AND relation.relkind IN ('r','p','v','m','f'))`, spec.Schema.String(), grant.Table).Scan(&exists); err != nil {
				return fmt.Errorf("migrate: grants: проверка таблицы %s: %w", grant.Table, err)
			}
			if !exists {
				return fmt.Errorf("migrate: grants: allowlisted таблица %q отсутствует в схеме %q", grant.Table, spec.Schema)
			}
			if len(grant.Privileges) > 0 {
				table := schema + "." + pgx.Identifier{grant.Table}.Sanitize()
				if _, err := tx.Exec(ctx, "GRANT "+strings.Join(grant.Privileges, ", ")+" ON TABLE "+table+" TO "+role); err != nil {
					return fmt.Errorf("migrate: grants: таблица %s для runtime %q: %w", grant.Table, runtime.Role, err)
				}
			}
		}
		for _, grant := range runtime.SequenceGrants {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname=$1 AND relation.relname=$2 AND relation.relkind='S')`, spec.Schema.String(), grant.Sequence).Scan(&exists); err != nil {
				return fmt.Errorf("migrate: grants: проверка sequence %s: %w", grant.Sequence, err)
			}
			if !exists {
				return fmt.Errorf("migrate: grants: allowlisted sequence %q отсутствует в схеме %q", grant.Sequence, spec.Schema)
			}
			if len(grant.Privileges) > 0 {
				sequence := schema + "." + pgx.Identifier{grant.Sequence}.Sanitize()
				if _, err := tx.Exec(ctx, "GRANT "+strings.Join(grant.Privileges, ", ")+" ON SEQUENCE "+sequence+" TO "+role); err != nil {
					return fmt.Errorf("migrate: grants: sequence %s для runtime %q: %w", grant.Sequence, runtime.Role, err)
				}
			}
		}
	}
	return nil
}

func ensureRole(ctx context.Context, tx pgx.Tx, name string, login bool, password string) error {
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", name).Scan(&exists); err != nil {
		return err
	}
	identifier := pgx.Identifier{name}.Sanitize()
	if !exists {
		statement := "CREATE ROLE " + identifier + " NOLOGIN"
		if login {
			var err error
			statement, err = roleStatement(ctx, tx, "CREATE ROLE", name, password)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
		if login {
			if _, err := tx.Exec(ctx, "ALTER ROLE "+identifier+" LOGIN"); err != nil {
				return err
			}
		}
	} else if login {
		// Provision/prepare не является credential rotation API. Существующий
		// login никогда не получает новый пароль из повторного запуска.
		var canLogin bool
		if err := tx.QueryRow(ctx, "SELECT rolcanlogin FROM pg_roles WHERE rolname=$1", name).Scan(&canLogin); err != nil {
			return err
		}
		if !canLogin {
			return errors.New("существующая managed role не является login role")
		}
	}
	if login {
		// Для новой роли LOGIN и password устанавливаются в CREATE ROLE выше.
		// Существующие пароли не читаются (PostgreSQL их не раскрывает) и не меняются.
	} else if _, err := tx.Exec(ctx, "ALTER ROLE "+identifier+" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
		return err
	}
	return nil
}

// roleStatement делегирует quoting литерала пароля PostgreSQL; пароль не
// включается в текст ошибки, который возвращается вызывающему коду.
func roleStatement(ctx context.Context, tx pgx.Tx, verb, role, password string) (string, error) {
	if _, err := tx.Exec(ctx, "SELECT set_config('leadgen.provision.role', $1, true), set_config('leadgen.provision.password', $2, true)", role, password); err != nil {
		return "", errors.New("migrate: provision: не удалось сформировать команду роли")
	}
	// Пароль передаётся только bind-параметром в set_config; SQL-текст команды,
	// который может попасть в логи PostgreSQL, содержит лишь вызов current_setting.
	return "DO $provision$ BEGIN EXECUTE format('" + verb + " %I PASSWORD %L', current_setting('leadgen.provision.role'), current_setting('leadgen.provision.password')); END $provision$", nil
}

func normalizeRoleMemberships(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	managed := []string{spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole}
	for _, runtime := range spec.AdditionalRuntimes {
		managed = append(managed, runtime.Role)
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT granted.rolname, member.rolname
FROM pg_auth_members membership
JOIN pg_roles granted ON granted.oid=membership.roleid
JOIN pg_roles member ON member.oid=membership.member
WHERE granted.rolname=ANY($1::text[]) OR member.rolname=ANY($1::text[])
ORDER BY granted.rolname, member.rolname`, managed)
	if err != nil {
		return err
	}
	type membership struct{ granted, member string }
	var memberships []membership
	for rows.Next() {
		var edge membership
		if err := rows.Scan(&edge.granted, &edge.member); err != nil {
			rows.Close()
			return err
		}
		memberships = append(memberships, edge)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, edge := range memberships {
		if _, err := tx.Exec(ctx, "REVOKE "+pgx.Identifier{edge.granted}.Sanitize()+" FROM "+pgx.Identifier{edge.member}.Sanitize()+" CASCADE"); err != nil {
			return err
		}
	}
	return nil
}

// clearOwnerDefaultACLs removes only stale default grants belonging to this
// managed NOLOGIN owner. Other roles' defaults and database/public ACLs are not
// changed. This prevents global or foreign-schema defaults leaking on later DDL.
func clearOwnerDefaultACLs(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	rows, err := tx.Query(ctx, `SELECT defaults.defaclobjtype, COALESCE(namespace.nspname,''), acl.grantee,
CASE WHEN acl.grantee=0 THEN 'PUBLIC' ELSE pg_get_userbyid(acl.grantee) END
FROM pg_default_acl defaults LEFT JOIN pg_namespace namespace ON namespace.oid=defaults.defaclnamespace
CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl
WHERE defaults.defaclrole=(SELECT oid FROM pg_roles WHERE rolname=$1)
  AND acl.grantee<>(SELECT oid FROM pg_roles WHERE rolname=$1)
ORDER BY defaults.defaclobjtype, namespace.nspname, acl.grantee`, spec.OwnerRole)
	if err != nil {
		return fmt.Errorf("migrate: provision: чтение owner default ACL: %w", err)
	}
	type defaultGrant struct {
		objectType byte
		schema     string
		grantee    uint32
		principal  string
	}
	var grants []defaultGrant
	for rows.Next() {
		var item defaultGrant
		if err := rows.Scan(&item.objectType, &item.schema, &item.grantee, &item.principal); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: provision: чтение owner default ACL: %w", err)
		}
		if item.grantee == 0 {
			item.principal = "PUBLIC"
		}
		grants = append(grants, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: provision: перечисление owner default ACL: %w", err)
	}
	rows.Close()
	for _, grant := range grants {
		objectType := map[byte]string{'r': "TABLES", 'S': "SEQUENCES", 'f': "ROUTINES", 'T': "TYPES", 'n': "SCHEMAS"}[grant.objectType]
		if objectType == "" {
			return fmt.Errorf("migrate: provision: неподдерживаемый pg_default_acl.defaclobjtype %q; cleanup остановлен", grant.objectType)
		}
		statement := "ALTER DEFAULT PRIVILEGES FOR ROLE " + pgx.Identifier{spec.OwnerRole}.Sanitize()
		if grant.schema != "" {
			statement += " IN SCHEMA " + pgx.Identifier{grant.schema}.Sanitize()
		}
		statement += " REVOKE ALL PRIVILEGES ON " + objectType + " FROM " + grantPrincipal(grant.principal)
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: provision: очистка default ACL owner %q grantee %q scope %q: %w", spec.OwnerRole, grant.principal, grant.schema, err)
		}
	}
	return nil
}

// normalizeManagedACLs removes old explicit grants from the managed schema only.
// Database ACLs, public schema ACLs and all objects outside the managed schema are
// deliberately untouched. The owner remains the sole direct grantee at this phase;
// GrantRuntimeSchema adds back the runtime allowlist after migrations.
func normalizeManagedACLs(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	// pg_proc.proacl=NULL не раскрывает эффективный PostgreSQL default EXECUTE
	// для PUBLIC. Отзываем его явно только в managed schema, иначе любой runtime
	// login этой базы сможет выполнять target-routines через PUBLIC, минуя свой
	// table grant allowlist.
	if _, err := tx.Exec(ctx, "REVOKE ALL PRIVILEGES ON ALL ROUTINES IN SCHEMA "+pgx.Identifier{spec.Schema.String()}.Sanitize()+" FROM PUBLIC CASCADE"); err != nil {
		return fmt.Errorf("migrate: ACL: отзыв PUBLIC routine privileges схемы %q: %w", spec.Schema, err)
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT acl.grantee,
CASE WHEN acl.grantee=0 THEN 'PUBLIC' ELSE pg_get_userbyid(acl.grantee) END
FROM (
 SELECT namespace.nspowner AS owner_oid, namespace.nspacl AS acl FROM pg_namespace namespace WHERE namespace.nspname=$1
 UNION ALL
 SELECT relation.relowner, relation.relacl FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1
 UNION ALL
 SELECT procedure.proowner, procedure.proacl FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace WHERE namespace.nspname=$1
 UNION ALL
 SELECT type.typowner, type.typacl FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace WHERE namespace.nspname=$1
	 UNION ALL
 SELECT relation.relowner, attribute.attacl FROM pg_attribute attribute JOIN pg_class relation ON relation.oid=attribute.attrelid JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND attribute.attacl IS NOT NULL
) object_acl(owner_oid, acl)
CROSS JOIN LATERAL aclexplode(object_acl.acl) acl
WHERE acl.grantee<>object_acl.owner_oid`, spec.Schema.String())
	if err != nil {
		return fmt.Errorf("migrate: ACL: перечисление grantees схемы %q: %w", spec.Schema, err)
	}
	var grantees []string
	for rows.Next() {
		var oid uint32
		var name string
		if err := rows.Scan(&oid, &name); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: ACL: чтение grantee схемы %q: %w", spec.Schema, err)
		}
		if oid == 0 {
			name = "PUBLIC"
		}
		grantees = append(grantees, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: ACL: перечисление grantees схемы %q: %w", spec.Schema, err)
	}
	rows.Close()
	grantees = uniqueStrings(append(grantees, "PUBLIC"))
	typeNames, err := managedTypeNames(ctx, tx, spec.Schema)
	if err != nil {
		return err
	}
	schema := pgx.Identifier{spec.Schema.String()}.Sanitize()
	for _, grantee := range grantees {
		principal := grantPrincipal(grantee)
		for _, statement := range []string{
			"REVOKE ALL PRIVILEGES ON SCHEMA " + schema + " FROM " + principal + " CASCADE",
			"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA " + schema + " FROM " + principal + " CASCADE",
			"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA " + schema + " FROM " + principal + " CASCADE",
		} {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return fmt.Errorf("migrate: ACL: отзыв grantee %q в схеме %q: %w", grantee, spec.Schema, err)
			}
		}
		if err := revokeRoutinePrivileges(ctx, tx, spec.Schema, spec.OwnedRoutines, principal); err != nil {
			return err
		}
		for _, name := range typeNames {
			statement := "REVOKE USAGE ON TYPE " + pgx.Identifier{spec.Schema.String(), name}.Sanitize() + " FROM " + principal + " CASCADE"
			if _, err := tx.Exec(ctx, statement); err != nil {
				return fmt.Errorf("migrate: ACL: отзыв TYPE %q grantee %q: %w", name, grantee, err)
			}
		}
		if err := revokeColumnPrivileges(ctx, tx, spec.Schema, principal); err != nil {
			return err
		}
	}
	return nil
}

// managedTypeNames перечисляет все ACL-addressable types и row types. Массивные
// типы имеют typelem<>0, не допускают отдельного REVOKE и наследуют effective
// USAGE от типа элемента; verifier проверяет их effective privilege отдельно.
func managedTypeNames(ctx context.Context, tx pgx.Tx, schema Schema) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT typname,typtype,typisdefined
FROM pg_type WHERE typnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) AND typelem=0
ORDER BY typname`, schema.String())
	if err != nil {
		return nil, fmt.Errorf("migrate: ACL: перечисление types схемы %q: %w", schema, err)
	}
	var names []string
	for rows.Next() {
		var name string
		var kind byte
		var defined bool
		if err := rows.Scan(&name, &kind, &defined); err != nil {
			rows.Close()
			return nil, fmt.Errorf("migrate: ACL: чтение type схемы %q: %w", schema, err)
		}
		if !defined {
			rows.Close()
			return nil, fmt.Errorf("migrate: ACL: shell type %q схемы %q не поддерживается", name, schema)
		}
		switch kind {
		case 'b', 'c', 'd', 'e':
			names = append(names, name)
		default:
			rows.Close()
			return nil, fmt.Errorf("migrate: ACL: неподдерживаемый type kind %q у %q схемы %q", kind, name, schema)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("migrate: ACL: перечисление types схемы %q: %w", schema, err)
	}
	rows.Close()
	return names, nil
}

func revokeRoutinePrivileges(ctx context.Context, tx pgx.Tx, schema Schema, allowlist []RoutineRef, principal string) error {
	rows, err := tx.Query(ctx, `SELECT procedure.proname, procedure.prokind, pg_get_function_identity_arguments(procedure.oid)
FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace WHERE namespace.nspname=$1`, schema.String())
	if err != nil {
		return fmt.Errorf("migrate: ACL: чтение routines схемы %q: %w", schema, err)
	}
	type routineObject struct {
		ref     RoutineRef
		prokind byte
	}
	var routines []routineObject
	for rows.Next() {
		var item routineObject
		if err := rows.Scan(&item.ref.Name, &item.prokind, &item.ref.Args); err != nil {
			rows.Close()
			return err
		}
		switch item.prokind {
		case 'f', 'w':
			item.ref.Kind = "function"
		case 'p':
			item.ref.Kind = "procedure"
		case 'a':
			item.ref.Kind = "aggregate"
		default:
			rows.Close()
			return fmt.Errorf("migrate: ACL: неизвестный routine kind %q в схеме %q", item.prokind, schema)
		}
		if !containsRoutine(allowlist, item.ref) {
			rows.Close()
			return fmt.Errorf("migrate: ACL: routine %q(%s) отсутствует в ownership allowlist схемы %q", item.ref.Name, item.ref.Args, schema)
		}
		routines = append(routines, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range routines {
		var statement string
		if err := tx.QueryRow(ctx, "SELECT format('REVOKE ALL PRIVILEGES ON ROUTINE %I.%I(%s) FROM %s CASCADE', $1::text, $2::text, $3::text, $4::text)", schema.String(), item.ref.Name, item.ref.Args, principal).Scan(&statement); err != nil {
			return fmt.Errorf("migrate: ACL: формирование routine revoke: %w", err)
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: ACL: revoke routine %q: %w", item.ref.Name, err)
		}
	}
	return nil
}

func revokeColumnPrivileges(ctx context.Context, tx pgx.Tx, schema Schema, principal string) error {
	rows, err := tx.Query(ctx, `SELECT relation.relname, attribute.attname
FROM pg_attribute attribute JOIN pg_class relation ON relation.oid=attribute.attrelid
JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
WHERE namespace.nspname=$1 AND attribute.attacl IS NOT NULL AND acl.grantee<>relation.relowner`, schema.String())
	if err != nil {
		return fmt.Errorf("migrate: ACL: перечисление column grants схемы %q: %w", schema, err)
	}
	type column struct{ relation, name string }
	var columns []column
	for rows.Next() {
		var item column
		if err := rows.Scan(&item.relation, &item.name); err != nil {
			rows.Close()
			return err
		}
		columns = append(columns, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range columns {
		statement := "REVOKE ALL PRIVILEGES (" + pgx.Identifier{item.name}.Sanitize() + ") ON TABLE " + pgx.Identifier{schema.String(), item.relation}.Sanitize() + " FROM " + principal + " CASCADE"
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: ACL: revoke column %s.%s: %w", item.relation, item.name, err)
		}
	}
	return nil
}

func grantPrincipal(role string) string {
	if role == "PUBLIC" {
		return role
	}
	return pgx.Identifier{role}.Sanitize()
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsRoutine(values []RoutineRef, target RoutineRef) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func transferAllowlistedOwnership(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	if err := rejectUnsupportedSchemaObjects(ctx, tx, spec.Schema); err != nil {
		return err
	}
	allowedRelations := make(map[string]struct{}, len(spec.OwnedRelations))
	for _, name := range spec.OwnedRelations {
		allowedRelations[name] = struct{}{}
	}
	rows, err := tx.Query(ctx, `SELECT relation.relname, relation.relkind
FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname=$1
ORDER BY relation.relname`, spec.Schema.String())
	if err != nil {
		return fmt.Errorf("migrate: provision: перечисление relations схемы %q: %w", spec.Schema, err)
	}
	type relation struct {
		name string
		kind byte
	}
	var relations []relation
	for rows.Next() {
		var item relation
		if err := rows.Scan(&item.name, &item.kind); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: provision: чтение relation схемы %q: %w", spec.Schema, err)
		}
		switch item.kind {
		case 'r', 'p', 'v', 'm', 'S':
			if _, ok := allowedRelations[item.name]; !ok {
				rows.Close()
				return fmt.Errorf("migrate: provision: relation %q схемы %q отсутствует в ownership allowlist", item.name, spec.Schema)
			}
		case 'f':
			rows.Close()
			return fmt.Errorf("migrate: provision: foreign table %q схемы %q не поддерживается: ownership также зависит от внешнего server", item.name, spec.Schema)
		case 'i', 'I', 't': // Индексы и TOAST принадлежат родительской таблице.
		case 'c': // Row type передаётся вместе с relation/type ниже.
		default:
			rows.Close()
			return fmt.Errorf("migrate: provision: неподдерживаемый pg_class.relkind %q у объекта %q схемы %q", item.kind, item.name, spec.Schema)
		}
		relations = append(relations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: provision: перечисление relations схемы %q: %w", spec.Schema, err)
	}
	rows.Close()
	for _, item := range relations {
		verb := "ALTER TABLE"
		switch item.kind {
		case 'i', 'I', 't', 'c':
			continue
		case 'v':
			verb = "ALTER VIEW"
		case 'm':
			verb = "ALTER MATERIALIZED VIEW"
		case 'S':
			verb = "ALTER SEQUENCE"
		}
		statement := verb + " " + pgx.Identifier{spec.Schema.String(), item.name}.Sanitize() + " OWNER TO " + pgx.Identifier{spec.OwnerRole}.Sanitize()
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: provision: передача ownership relation %q.%q: %w", spec.Schema, item.name, err)
		}
	}

	allowedRoutines := make(map[RoutineRef]struct{}, len(spec.OwnedRoutines))
	for _, ref := range spec.OwnedRoutines {
		allowedRoutines[ref] = struct{}{}
	}
	rows, err = tx.Query(ctx, `SELECT procedure.proname, procedure.prokind, pg_get_function_identity_arguments(procedure.oid)
FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname=$1
ORDER BY procedure.proname, procedure.prokind, pg_get_function_identity_arguments(procedure.oid)`, spec.Schema.String())
	if err != nil {
		return fmt.Errorf("migrate: provision: перечисление functions схемы %q: %w", spec.Schema, err)
	}
	var routines []RoutineRef
	for rows.Next() {
		var ref RoutineRef
		var prokind byte
		if err := rows.Scan(&ref.Name, &prokind, &ref.Args); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: provision: чтение routine схемы %q: %w", spec.Schema, err)
		}
		switch prokind {
		case 'f', 'w':
			ref.Kind = "function"
		case 'p':
			ref.Kind = "procedure"
		case 'a':
			ref.Kind = "aggregate"
		default:
			rows.Close()
			return fmt.Errorf("migrate: provision: неподдерживаемый pg_proc.prokind %q у routine %q схемы %q", prokind, ref.Name, spec.Schema)
		}
		if _, ok := allowedRoutines[ref]; !ok {
			rows.Close()
			return fmt.Errorf("migrate: provision: routine %q %q(%s) схемы %q отсутствует в ownership allowlist", ref.Kind, ref.Name, ref.Args, spec.Schema)
		}
		routines = append(routines, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: provision: перечисление functions схемы %q: %w", spec.Schema, err)
	}
	rows.Close()
	for _, ref := range routines {
		var statement string
		verb := map[string]string{"function": "FUNCTION", "procedure": "PROCEDURE", "aggregate": "AGGREGATE"}[ref.Kind]
		if err := tx.QueryRow(ctx, "SELECT format('ALTER "+verb+" %I.%I(%s) OWNER TO %I', $1::text, $2::text, $3::text, $4::text)", spec.Schema.String(), ref.Name, ref.Args, spec.OwnerRole).Scan(&statement); err != nil {
			return fmt.Errorf("migrate: provision: формирование ownership routine %q: %w", ref.Name, err)
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: provision: передача ownership routine %q: %w", ref.Name, err)
		}
	}
	if err := transferAllowlistedTypes(ctx, tx, spec); err != nil {
		return err
	}
	if err := transferAllowlistedCollations(ctx, tx, spec); err != nil {
		return err
	}
	return nil
}

// Поддерживаются relations, routines (function/window/procedure/aggregate),
// standalone types (base/composite/domain/enum/range/multirange) и collations.
// Индексы, TOAST relations и row types следуют ownership родителя. Все иные
// namespace-owned классы ниже запрещены до явного добавления к политике.
func transferAllowlistedTypes(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	allowed := make(map[string]struct{}, len(spec.OwnedTypes))
	for _, name := range spec.OwnedTypes {
		allowed[name] = struct{}{}
	}
	rows, err := tx.Query(ctx, `SELECT type.typname
FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
WHERE namespace.nspname=$1 AND type.typtype IN ('b','c','d','e')
  AND type.typisdefined AND type.typelem=0
  AND (type.typrelid=0 OR EXISTS (SELECT 1 FROM pg_class composite WHERE composite.oid=type.typrelid AND composite.relkind='c'))
ORDER BY type.typname`, spec.Schema.String())
	if err != nil {
		return fmt.Errorf("migrate: provision: перечисление types схемы %q: %w", spec.Schema, err)
	}
	var types []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: provision: чтение type схемы %q: %w", spec.Schema, err)
		}
		if _, ok := allowed[name]; !ok {
			rows.Close()
			return fmt.Errorf("migrate: provision: type/domain/range %q схемы %q отсутствует в ownership allowlist", name, spec.Schema)
		}
		types = append(types, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: provision: перечисление types схемы %q: %w", spec.Schema, err)
	}
	rows.Close()
	for _, name := range types {
		statement := "ALTER TYPE " + pgx.Identifier{spec.Schema.String(), name}.Sanitize() + " OWNER TO " + pgx.Identifier{spec.OwnerRole}.Sanitize()
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: provision: передача ownership type %q: %w", name, err)
		}
	}
	return nil
}

func transferAllowlistedCollations(ctx context.Context, tx pgx.Tx, spec RoleSet) error {
	allowed := make(map[string]struct{}, len(spec.OwnedCollations))
	for _, name := range spec.OwnedCollations {
		allowed[name] = struct{}{}
	}
	rows, err := tx.Query(ctx, `SELECT coll.collname FROM pg_collation coll
JOIN pg_namespace namespace ON namespace.oid=coll.collnamespace
WHERE namespace.nspname=$1 ORDER BY coll.collname`, spec.Schema.String())
	if err != nil {
		return fmt.Errorf("migrate: provision: перечисление collations схемы %q: %w", spec.Schema, err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: provision: чтение collation схемы %q: %w", spec.Schema, err)
		}
		if _, ok := allowed[name]; !ok {
			rows.Close()
			return fmt.Errorf("migrate: provision: collation %q схемы %q отсутствует в ownership allowlist", name, spec.Schema)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: provision: перечисление collations схемы %q: %w", spec.Schema, err)
	}
	rows.Close()
	for _, name := range names {
		statement := "ALTER COLLATION " + pgx.Identifier{spec.Schema.String(), name}.Sanitize() + " OWNER TO " + pgx.Identifier{spec.OwnerRole}.Sanitize()
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("migrate: provision: передача ownership collation %q: %w", name, err)
		}
	}
	return nil
}

func rejectUnsupportedSchemaObjects(ctx context.Context, tx pgx.Tx, schema Schema) error {
	checks := []struct {
		label string
		query string
	}{
		{"range/multirange type", `SELECT type.typname FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace WHERE namespace.nspname=$1 AND type.typtype IN ('r','m') LIMIT 1`},
		{"pseudo type", `SELECT typname FROM pg_type WHERE typnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) AND typtype='p' AND typisdefined AND typelem=0 LIMIT 1`},
		{"operator", `SELECT oprname FROM pg_operator WHERE oprnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"operator class", `SELECT opcname FROM pg_opclass WHERE opcnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"operator family", `SELECT opfname FROM pg_opfamily WHERE opfnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"text search configuration", `SELECT cfgname FROM pg_ts_config WHERE cfgnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"text search dictionary", `SELECT dictname FROM pg_ts_dict WHERE dictnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"text search parser", `SELECT prsname FROM pg_ts_parser WHERE prsnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"text search template", `SELECT tmplname FROM pg_ts_template WHERE tmplnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"conversion", `SELECT conname FROM pg_conversion WHERE connamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
		{"extended statistics", `SELECT stxname FROM pg_statistic_ext WHERE stxnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1) LIMIT 1`},
	}
	for _, check := range checks {
		var name string
		err := tx.QueryRow(ctx, check.query, schema.String()).Scan(&name)
		if err == nil {
			return fmt.Errorf("migrate: provision: неподдерживаемый namespace object %s %q в схеме %q", check.label, name, schema)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("migrate: provision: проверка %s в схеме %q: %w", check.label, schema, err)
		}
	}
	return nil
}

func validateRoleSet(spec RoleSet) error {
	if err := validateRuntimeRoleScope(spec); err != nil {
		return fmt.Errorf("migrate: некорректный runtime identity: %w", err)
	}
	if strings.TrimSpace(spec.Schema.String()) == "" || strings.TrimSpace(spec.OwnerRole) == "" || strings.TrimSpace(spec.MigratorRole) == "" || strings.TrimSpace(spec.RuntimeRole) == "" {
		return errors.New("migrate: provision: схема и имена ролей обязательны")
	}
	if spec.OwnerRole == spec.MigratorRole || spec.OwnerRole == spec.RuntimeRole || spec.MigratorRole == spec.RuntimeRole {
		return errors.New("migrate: provision: owner, migrator и runtime должны быть разными ролями")
	}
	seenRoles := map[string]struct{}{spec.OwnerRole: {}, spec.MigratorRole: {}, spec.RuntimeRole: {}}
	for _, runtime := range spec.AdditionalRuntimes {
		if runtime.RuntimeScope != nil {
			expected, err := RuntimeRoleName(*runtime.RuntimeScope)
			if err != nil || expected != runtime.Role {
				return errors.New("migrate: дополнительная runtime role не соответствует generation scope")
			}
		}
		if strings.TrimSpace(runtime.Role) == "" {
			return errors.New("migrate: provision: имя дополнительной runtime-роли обязательно")
		}
		if _, exists := seenRoles[runtime.Role]; exists {
			return fmt.Errorf("migrate: provision: дублирующая runtime identity %q", runtime.Role)
		}
		seenRoles[runtime.Role] = struct{}{}
	}
	for _, grant := range spec.Grants {
		if err := validateTableGrant(grant); err != nil {
			return err
		}
	}
	for _, grant := range spec.SequenceGrants {
		if err := validateSequenceGrant(grant); err != nil {
			return err
		}
	}
	for _, runtime := range spec.AdditionalRuntimes {
		for _, grant := range runtime.Grants {
			if err := validateTableGrant(grant); err != nil {
				return err
			}
		}
		for _, grant := range runtime.SequenceGrants {
			if err := validateSequenceGrant(grant); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTableGrant(grant TableGrant) error {
	if strings.TrimSpace(grant.Table) == "" {
		return errors.New("migrate: provision: имя таблицы обязательно")
	}
	for _, privilege := range grant.Privileges {
		switch privilege {
		case "SELECT", "INSERT", "UPDATE", "DELETE":
		default:
			return fmt.Errorf("migrate: provision: неподдерживаемая table privilege %q", privilege)
		}
	}
	return nil
}

func validateSequenceGrant(grant SequenceGrant) error {
	if strings.TrimSpace(grant.Sequence) == "" {
		return errors.New("migrate: provision: имя sequence обязательно")
	}
	for _, privilege := range grant.Privileges {
		switch privilege {
		case "USAGE", "SELECT", "UPDATE":
		default:
			return fmt.Errorf("migrate: provision: неподдерживаемая sequence privilege %q", privilege)
		}
	}
	return nil
}

func runtimeIdentities(spec RoleSet) []RuntimeIdentity {
	identities := make([]RuntimeIdentity, 0, 1+len(spec.AdditionalRuntimes))
	identities = append(identities, RuntimeIdentity{Role: spec.RuntimeRole, Password: spec.RuntimePassword, Grants: spec.Grants, SequenceGrants: spec.SequenceGrants})
	identities = append(identities, spec.AdditionalRuntimes...)
	return identities
}
