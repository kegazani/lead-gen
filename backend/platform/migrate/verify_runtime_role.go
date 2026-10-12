package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VerifyRuntimeRole проверяет, что подключение использует ровно ожидаемую
// runtime login identity, не может принять owner/migrator и не имеет эффективных
// привилегий вне allowlist target schema. Database TEMP унаследованный от PUBLIC
// намеренно не проверяется и не отзывается.
func VerifyRuntimeRole(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	if pool == nil {
		return errors.New("migrate: verify runtime role: пул соединений не задан")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRoleSet(spec); err != nil {
		return fmt.Errorf("migrate: verify runtime role: некорректный target: %w", err)
	}
	var loginSafe bool
	if err := pool.QueryRow(ctx, `SELECT current_user=$1 AND session_user=$1
AND role.rolcanlogin AND NOT role.rolsuper AND NOT role.rolcreatedb
AND NOT role.rolcreaterole AND NOT role.rolreplication AND NOT role.rolbypassrls
AND NOT role.rolinherit
FROM pg_roles role WHERE role.rolname=current_user`, spec.RuntimeRole).Scan(&loginSafe); err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить identity")
	}
	if !loginSafe {
		return errors.New("migrate: verify runtime role: подключение не соответствует ожидаемой ограниченной login role")
	}
	var memberships bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_auth_members membership
WHERE membership.member=(SELECT oid FROM pg_roles WHERE rolname=current_user)
   OR membership.roleid=(SELECT oid FROM pg_roles WHERE rolname=current_user))`).Scan(&memberships); err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить memberships")
	}
	if memberships {
		return errors.New("migrate: verify runtime role: у runtime identity обнаружен membership")
	}
	var identitiesReady bool
	if err := pool.QueryRow(ctx, `SELECT owner.rolname IS NOT NULL AND NOT owner.rolcanlogin
AND NOT owner.rolsuper AND NOT owner.rolcreatedb AND NOT owner.rolcreaterole
AND NOT owner.rolreplication AND NOT owner.rolbypassrls AND NOT owner.rolinherit
AND migrator.rolcanlogin AND NOT migrator.rolsuper AND NOT migrator.rolcreatedb
AND NOT migrator.rolcreaterole AND NOT migrator.rolreplication AND NOT migrator.rolbypassrls
AND NOT migrator.rolinherit
AND (SELECT count(*)=1 FROM pg_auth_members membership
     WHERE membership.roleid=owner.oid AND membership.member=migrator.oid
       AND NOT membership.admin_option AND membership.set_option AND NOT membership.inherit_option)
AND NOT EXISTS (
  SELECT 1 FROM pg_auth_members membership
  WHERE (membership.member=owner.oid OR membership.roleid=owner.oid
         OR membership.member=migrator.oid OR membership.roleid=migrator.oid)
    AND NOT (membership.roleid=owner.oid AND membership.member=migrator.oid
             AND NOT membership.admin_option AND membership.set_option AND NOT membership.inherit_option))
FROM pg_roles owner CROSS JOIN pg_roles migrator
WHERE owner.rolname=$1 AND migrator.rolname=$2`, spec.OwnerRole, spec.MigratorRole).Scan(&identitiesReady); err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить managed identities")
	}
	if !identitiesReady {
		return errors.New("migrate: verify runtime role: managed owner/migrator roles или membership настроены неверно")
	}
	var ownerMatch, schemaUsage, schemaCreate, databaseCreate bool
	if err := pool.QueryRow(ctx, `SELECT namespace.nspowner=(SELECT oid FROM pg_roles WHERE rolname=$2),
has_schema_privilege(current_user,namespace.oid,'USAGE'),
has_schema_privilege(current_user,namespace.oid,'CREATE')
FROM pg_namespace namespace WHERE namespace.nspname=$1`, spec.Schema.String(), spec.OwnerRole).Scan(&ownerMatch, &schemaUsage, &schemaCreate); err != nil {
		return errors.New("migrate: verify runtime role: target schema не найдена или недоступна")
	}
	if !ownerMatch || !schemaUsage || schemaCreate {
		return errors.New("migrate: verify runtime role: target schema ownership/privileges не соответствуют runtime policy")
	}
	if err := pool.QueryRow(ctx, `SELECT has_database_privilege(current_user,current_database(),'CREATE')`).Scan(&databaseCreate); err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить database privileges")
	}
	if databaseCreate {
		return errors.New("migrate: verify runtime role: у runtime identity есть database CREATE")
	}
	if err := verifyRuntimeOwnershipScope(ctx, pool); err != nil {
		return err
	}
	if err := verifyRuntimeSchemaScope(ctx, pool, spec); err != nil {
		return err
	}
	if err := verifyRuntimeRelationScope(ctx, pool, spec); err != nil {
		return err
	}
	if err := verifyRuntimeSequenceScope(ctx, pool, spec); err != nil {
		return err
	}
	if err := verifyRuntimeTypeScope(ctx, pool); err != nil {
		return err
	}
	if err := verifyRuntimeRoutineScope(ctx, pool); err != nil {
		return err
	}
	return nil
}

// VerifyRuntimeRole также проверяет, что runtime identity не владеет ни одним
// из классов объектов, которые ProvisionSchema допускает в managed schema:
// схемами, relations (включая индексы, TOAST и row types), routines,
// types/domains и collations.
func verifyRuntimeOwnershipScope(ctx context.Context, pool *pgxpool.Pool) error {
	var owns bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_namespace namespace WHERE namespace.nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
    AND namespace.nspname NOT IN ('pg_catalog','information_schema')
    AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
    AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
  UNION ALL
  SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
    WHERE relation.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
      AND namespace.nspname NOT IN ('pg_catalog','information_schema')
      AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
      AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
  UNION ALL
  SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
    WHERE procedure.proowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
      AND namespace.nspname NOT IN ('pg_catalog','information_schema')
      AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
      AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
  UNION ALL
  SELECT 1 FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
    WHERE type.typowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
      AND namespace.nspname NOT IN ('pg_catalog','information_schema')
      AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
      AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
  UNION ALL
  SELECT 1 FROM pg_collation coll JOIN pg_namespace namespace ON namespace.oid=coll.collnamespace
    WHERE coll.collowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
      AND namespace.nspname NOT IN ('pg_catalog','information_schema')
      AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
      AND left(namespace.nspname,length('pg_toast'))<>'pg_toast')`).Scan(&owns)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить ownership поддерживаемых object classes")
	}
	if owns {
		return errors.New("migrate: verify runtime role: runtime identity владеет schema/object")
	}
	return nil
}

func verifyRuntimeSchemaScope(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	var outside bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_namespace namespace
WHERE namespace.nspname<>$1
  AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
  AND (has_schema_privilege(current_user,namespace.oid,'CREATE')
       OR (namespace.nspname<>'public' AND has_schema_privilege(current_user,namespace.oid,'USAGE'))))`, spec.Schema.String()).Scan(&outside)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить доступ к другим schemas")
	}
	if outside {
		return errors.New("migrate: verify runtime role: у runtime identity есть privileges вне target schema")
	}
	return nil
}

func verifyRuntimeRelationScope(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	if err := verifyRuntimeRelationKinds(ctx, pool, spec.Schema); err != nil {
		return err
	}
	expected := make(map[string]map[string]bool, len(spec.Grants))
	for _, grant := range spec.Grants {
		set := make(map[string]bool, len(grant.Privileges))
		for _, privilege := range grant.Privileges {
			set[privilege] = true
		}
		expected[grant.Table] = set
	}
	return verifyRuntimeRelations(ctx, pool, spec, expected)
}

func verifyRuntimeRelationKinds(ctx context.Context, pool *pgxpool.Pool, schema Schema) error {
	rows, err := pool.Query(ctx, `SELECT relation.relname,relation.relkind
FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname=$1`, schema.String())
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить relation kinds target schema")
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var kind byte
		if err := rows.Scan(&name, &kind); err != nil {
			return errors.New("migrate: verify runtime role: ошибка чтения relation kind")
		}
		if !supportedProvisionRelationKind(kind) {
			return fmt.Errorf("migrate: verify runtime role: неподдерживаемый relation kind %q у %q в target schema", kind, name)
		}
	}
	if err := rows.Err(); err != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления relation kinds")
	}
	return nil
}

func supportedProvisionRelationKind(kind byte) bool {
	switch kind {
	case 'r', 'p', 'v', 'm', 'S', 'i', 'I', 't', 'c':
		return true
	default:
		return false
	}
}

func verifyRuntimeRelations(ctx context.Context, pool *pgxpool.Pool, spec RoleSet, expected map[string]map[string]bool) error {
	rows, err := pool.Query(ctx, `SELECT relation.relname, namespace.nspname, relation.relkind, relation.oid
FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE relation.relkind IN ('r','p','v','m','f')
  AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
ORDER BY namespace.nspname,relation.relname`)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось перечислить relations")
	}
	privileges := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}
	var pgVersion int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&pgVersion); err != nil {
		rows.Close()
		return errors.New("migrate: verify runtime role: не удалось определить версию PostgreSQL")
	}
	if pgVersion >= 170000 {
		privileges = append(privileges, "MAINTAIN")
	}
	type relationInfo struct {
		name, schema string
		kind         byte
		oid          uint32
	}
	var relations []relationInfo
	for rows.Next() {
		var relation relationInfo
		if err := rows.Scan(&relation.name, &relation.schema, &relation.kind, &relation.oid); err != nil {
			rows.Close()
			return errors.New("migrate: verify runtime role: ошибка чтения relations")
		}
		relations = append(relations, relation)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления relations")
	}
	for _, relation := range relations {
		allowed := map[string]bool(nil)
		if relation.schema == spec.Schema.String() {
			allowed = expected[relation.name]
		}
		for _, privilege := range privileges {
			var has bool
			if err := pool.QueryRow(ctx, "SELECT has_table_privilege(current_user,$1::oid,$2)", relation.oid, privilege).Scan(&has); err != nil {
				return fmt.Errorf("migrate: verify runtime role: ошибка проверки table privilege %s: %w", privilege, err)
			}
			if has != (allowed != nil && allowed[privilege]) {
				return errors.New("migrate: verify runtime role: table privilege выходит за allowlist target schema")
			}
		}
		if err := verifyRuntimeColumnPrivileges(ctx, pool, relation.oid, allowed); err != nil {
			return err
		}
	}
	return nil
}

func verifyRuntimeColumnPrivileges(ctx context.Context, pool *pgxpool.Pool, relationOID uint32, allowed map[string]bool) error {
	rows, err := pool.Query(ctx, `SELECT attnum FROM pg_attribute
WHERE attrelid=$1 AND attnum>0 AND NOT attisdropped`, relationOID)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось перечислить columns")
	}
	var attributes []int16
	for rows.Next() {
		var attnum int16
		if err := rows.Scan(&attnum); err != nil {
			rows.Close()
			return errors.New("migrate: verify runtime role: ошибка чтения columns")
		}
		attributes = append(attributes, attnum)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления columns")
	}
	columnPrivileges := []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}
	for _, attribute := range attributes {
		for _, privilege := range columnPrivileges {
			var has bool
			if err := pool.QueryRow(ctx, "SELECT has_column_privilege(current_user,$1::oid,$2::smallint,$3)", relationOID, attribute, privilege).Scan(&has); err != nil {
				return errors.New("migrate: verify runtime role: ошибка проверки column privileges")
			}
			if has != (allowed != nil && allowed[privilege]) {
				return errors.New("migrate: verify runtime role: column privilege выходит за allowlist target schema")
			}
		}
	}
	return nil
}

func verifyRuntimeSequenceScope(ctx context.Context, pool *pgxpool.Pool, spec RoleSet) error {
	expected := make(map[string]map[string]bool, len(spec.SequenceGrants))
	for _, grant := range spec.SequenceGrants {
		set := make(map[string]bool, len(grant.Privileges))
		for _, privilege := range grant.Privileges {
			set[privilege] = true
		}
		expected[grant.Sequence] = set
	}
	rows, err := pool.Query(ctx, `SELECT relation.relname, namespace.nspname, relation.oid
FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE relation.relkind='S' AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'`)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось перечислить sequences")
	}
	type sequenceInfo struct {
		name, schema string
		oid          uint32
	}
	var sequences []sequenceInfo
	for rows.Next() {
		var sequence sequenceInfo
		if err := rows.Scan(&sequence.name, &sequence.schema, &sequence.oid); err != nil {
			rows.Close()
			return errors.New("migrate: verify runtime role: ошибка чтения sequences")
		}
		sequences = append(sequences, sequence)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления sequences")
	}
	for _, sequence := range sequences {
		allowed := map[string]bool(nil)
		if sequence.schema == spec.Schema.String() {
			allowed = expected[sequence.name]
		}
		for _, privilege := range []string{"USAGE", "SELECT", "UPDATE"} {
			var has bool
			if err := pool.QueryRow(ctx, "SELECT has_sequence_privilege(current_user,$1::oid,$2)", sequence.oid, privilege).Scan(&has); err != nil {
				return errors.New("migrate: verify runtime role: ошибка проверки sequence privileges")
			}
			if has != (allowed != nil && allowed[privilege]) {
				return errors.New("migrate: verify runtime role: sequence privilege выходит за allowlist target schema")
			}
		}
	}
	return nil
}

// Runtime RoleSet сейчас не выдаёт TYPE/DOMAIN USAGE. Проверка standalone
// user-defined types/domains работает fail-closed. Массивы и row types relations
// — производные catalog objects: их доступ определяется типом элемента либо
// allowlist родительской relation, а не отдельным runtime grant.
func verifyRuntimeTypeScope(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `SELECT namespace.nspname,type.typname,type.typtype,type.typisdefined,type.oid,
type.typelem,type.typrelid,COALESCE(composite.relkind,' ')
FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
LEFT JOIN pg_class composite ON composite.oid=type.typrelid
WHERE namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
ORDER BY namespace.nspname,type.typname`)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось перечислить user-defined types")
	}
	typeInfo := struct {
		schema, name string
		kind         byte
		defined      bool
		oid          uint32
		elementOID   uint32
		relationOID  uint32
		relationKind byte
	}{}
	for rows.Next() {
		if err := rows.Scan(&typeInfo.schema, &typeInfo.name, &typeInfo.kind, &typeInfo.defined, &typeInfo.oid, &typeInfo.elementOID, &typeInfo.relationOID, &typeInfo.relationKind); err != nil {
			rows.Close()
			return errors.New("migrate: verify runtime role: ошибка чтения type catalog")
		}
		if !typeInfo.defined {
			rows.Close()
			return errors.New("migrate: verify runtime role: обнаружен shell type")
		}
		if !supportedProvisionTypeKind(typeInfo.kind) {
			rows.Close()
			return fmt.Errorf("migrate: verify runtime role: неподдерживаемый type kind %q в schema %q", typeInfo.kind, typeInfo.schema)
		}
		if typeInfo.elementOID != 0 || (typeInfo.relationOID != 0 && typeInfo.relationKind != 'c') {
			// Автоматический массив наследует тип элемента; table/view row type
			// наследует границу ACL своей родительской relation.
			continue
		}
		var usage bool
		if err := pool.QueryRow(ctx, "SELECT has_type_privilege(current_user,$1::oid,'USAGE')", typeInfo.oid).Scan(&usage); err != nil {
			rows.Close()
			return errors.New("migrate: verify runtime role: не удалось проверить type/domain USAGE")
		}
		if usage {
			rows.Close()
			return fmt.Errorf("migrate: verify runtime role: runtime identity имеет неразрешённый USAGE на type %q.%q", typeInfo.schema, typeInfo.name)
		}
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления type catalog")
	}
	return nil
}

func supportedProvisionTypeKind(kind byte) bool {
	switch kind {
	case 'b', 'c', 'd', 'e':
		return true
	default:
		return false
	}
}

func verifyRuntimeRoutineScope(ctx context.Context, pool *pgxpool.Pool) error {
	// Runtime RoleSet пока не объявляет routine grants: trigger routines в своей
	// схеме исполняются PostgreSQL как часть разрешённых table operations, но
	// прямой EXECUTE runtime identity не выдаётся.
	rows, err := pool.Query(ctx, `SELECT namespace.nspname,procedure.proname,procedure.prokind,
has_function_privilege(current_user,procedure.oid,'EXECUTE')
FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast'
ORDER BY namespace.nspname,procedure.proname`)
	if err != nil {
		return errors.New("migrate: verify runtime role: не удалось проверить routine privileges")
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name string
		var kind byte
		var execute bool
		if err := rows.Scan(&schema, &name, &kind, &execute); err != nil {
			return errors.New("migrate: verify runtime role: ошибка чтения routine privileges")
		}
		switch kind {
		case 'f', 'w':
		case 'p':
		case 'a':
		default:
			return errors.New("migrate: verify runtime role: неизвестный routine kind")
		}
		if execute {
			return fmt.Errorf("migrate: verify runtime role: runtime identity имеет неразрешённый EXECUTE на routine %q.%q", schema, name)
		}
	}
	if err := rows.Err(); err != nil {
		return errors.New("migrate: verify runtime role: ошибка перечисления routine privileges")
	}
	return nil
}
