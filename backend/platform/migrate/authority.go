package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type migrationAuthorityQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// verifyMigrationAuthority fails closed unless SET ROLE is exactly the NOLOGIN
// owner of targetSchema and its effective DDL/data authority is confined there.
// PUBLIC database privileges are never modified; effective CREATE elsewhere,
// including inherited PUBLIC CREATE, is a blocker.
func verifyMigrationAuthority(ctx context.Context, query migrationAuthorityQuerier, targetSchema Schema, ownerRole string) error {
	var roleMembershipOptionsAvailable bool
	if err := query.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer >= 160000").Scan(&roleMembershipOptionsAvailable); err != nil {
		return fmt.Errorf("migrate: authority: PostgreSQL version check: %w", err)
	}
	if !roleMembershipOptionsAvailable {
		return errors.New("migrate: authority: PostgreSQL 16+ требуется для проверки SET/INHERIT membership options")
	}
	var validRole bool
	if err := query.QueryRow(ctx, `SELECT current_user=$1
AND NOT role.rolcanlogin AND NOT role.rolsuper AND NOT role.rolbypassrls
AND NOT role.rolcreatedb AND NOT role.rolcreaterole AND NOT role.rolreplication AND NOT role.rolinherit
FROM pg_roles role WHERE role.rolname=current_user`, ownerRole).Scan(&validRole); err != nil {
		return fmt.Errorf("migrate: authority: чтение owner role: %w", err)
	}
	if !validRole {
		return fmt.Errorf("migrate: authority: current_role не является ожидаемым NOLOGIN owner %q", ownerRole)
	}
	var migratorRoleSafe bool
	if err := query.QueryRow(ctx, `SELECT role.rolcanlogin AND NOT role.rolsuper AND NOT role.rolbypassrls
AND NOT role.rolcreatedb AND NOT role.rolcreaterole AND NOT role.rolreplication
FROM pg_roles role WHERE role.rolname=session_user`).Scan(&migratorRoleSafe); err != nil {
		return fmt.Errorf("migrate: authority: чтение migration login: %w", err)
	}
	if !migratorRoleSafe {
		return errors.New("migrate: authority: session_user не является ограниченным migration login")
	}
	var schemaOwnerMatches bool
	if err := query.QueryRow(ctx, `SELECT namespace.nspowner=role.oid
FROM pg_namespace namespace JOIN pg_roles role ON role.rolname=$2
WHERE namespace.nspname=$1`, targetSchema.String(), ownerRole).Scan(&schemaOwnerMatches); err != nil {
		return fmt.Errorf("migrate: authority: целевая схема %q отсутствует или недоступна: %w", targetSchema, err)
	}
	if !schemaOwnerMatches {
		return fmt.Errorf("migrate: authority: роль %q не владеет целевой схемой %q", ownerRole, targetSchema)
	}
	var databaseCreate bool
	if err := query.QueryRow(ctx, `SELECT has_database_privilege(current_user,current_database(),'CREATE')`).Scan(&databaseCreate); err != nil {
		return fmt.Errorf("migrate: authority: проверка database CREATE: %w", err)
	}
	if databaseCreate {
		return fmt.Errorf("migrate: authority: owner %q имеет effective CREATE на database вне схемы %q", ownerRole, targetSchema)
	}

	checks := []struct {
		label string
		query string
	}{
		{"membership вне migrator→owner", `SELECT EXISTS (
SELECT 1 FROM pg_auth_members membership
JOIN pg_roles owner_role ON owner_role.oid=membership.roleid
JOIN pg_roles member_role ON member_role.oid=membership.member
WHERE (owner_role.rolname=current_user OR member_role.rolname=current_user
       OR owner_role.rolname=session_user OR member_role.rolname=session_user)
  AND NOT (owner_role.rolname=current_user AND member_role.rolname=session_user
           AND NOT membership.admin_option AND membership.set_option AND NOT membership.inherit_option))`},
		{"ownership relation за пределами target", `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND relation.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership routine за пределами target", `SELECT EXISTS (
SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND procedure.proowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership type за пределами target", `SELECT EXISTS (
SELECT 1 FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND type.typowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership collation за пределами target", `SELECT EXISTS (
SELECT 1 FROM pg_collation coll JOIN pg_namespace namespace ON namespace.oid=coll.collnamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND coll.collowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership operator/opclass/statistics за пределами target", `SELECT EXISTS (
SELECT 1 FROM pg_operator object JOIN pg_namespace namespace ON namespace.oid=object.oprnamespace WHERE namespace.nspname<>$1 AND object.oprowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_opclass object JOIN pg_namespace namespace ON namespace.oid=object.opcnamespace WHERE namespace.nspname<>$1 AND object.opcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_opfamily object JOIN pg_namespace namespace ON namespace.oid=object.opfnamespace WHERE namespace.nspname<>$1 AND object.opfowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_statistic_ext object JOIN pg_namespace namespace ON namespace.oid=object.stxnamespace WHERE namespace.nspname<>$1 AND object.stxowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership auxiliary namespace objects вне target", `SELECT EXISTS (
SELECT 1 FROM pg_ts_config object JOIN pg_namespace namespace ON namespace.oid=object.cfgnamespace WHERE namespace.nspname<>$1 AND object.cfgowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_ts_dict object JOIN pg_namespace namespace ON namespace.oid=object.dictnamespace WHERE namespace.nspname<>$1 AND object.dictowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_conversion object JOIN pg_namespace namespace ON namespace.oid=object.connamespace WHERE namespace.nspname<>$1 AND object.conowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"ownership global objects outside target", `SELECT EXISTS (
SELECT 1 FROM pg_database WHERE datdba=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_tablespace WHERE spcowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_language WHERE lanowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_foreign_server WHERE srvowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_foreign_data_wrapper WHERE fdwowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_publication WHERE pubowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_subscription WHERE subowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL SELECT 1 FROM pg_largeobject_metadata WHERE lomowner=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"direct schema grant вне target", `SELECT EXISTS (
SELECT 1 FROM pg_namespace namespace CROSS JOIN LATERAL aclexplode(namespace.nspacl) acl
WHERE namespace.nspname<>$1 AND acl.grantee=(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"effective CREATE вне target schema", `SELECT EXISTS (
SELECT 1 FROM pg_namespace namespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND has_schema_privilege(current_user,namespace.oid,'CREATE'))`},
		{"direct table/column grant вне target", `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
CROSS JOIN LATERAL aclexplode(relation.relacl) acl
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND acl.grantee=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL
SELECT 1 FROM pg_attribute attribute JOIN pg_class relation ON relation.oid=attribute.attrelid
JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND acl.grantee=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL
SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
CROSS JOIN LATERAL aclexplode(procedure.proacl) acl
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND acl.grantee=(SELECT oid FROM pg_roles WHERE rolname=current_user)
UNION ALL
SELECT 1 FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
CROSS JOIN LATERAL aclexplode(type.typacl) acl
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND acl.grantee=(SELECT oid FROM pg_roles WHERE rolname=current_user)
 )`},
		{"effective table privileges вне target", `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND relation.relkind IN ('r','p','v','m','f')
  AND (has_table_privilege(current_user,relation.oid,'SELECT') OR has_table_privilege(current_user,relation.oid,'INSERT')
    OR has_table_privilege(current_user,relation.oid,'UPDATE') OR has_table_privilege(current_user,relation.oid,'DELETE')
    OR has_table_privilege(current_user,relation.oid,'TRUNCATE') OR has_table_privilege(current_user,relation.oid,'TRIGGER')))`},
		{"effective sequence privileges вне target", `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND relation.relkind='S' AND (has_sequence_privilege(current_user,relation.oid,'USAGE')
    OR has_sequence_privilege(current_user,relation.oid,'SELECT') OR has_sequence_privilege(current_user,relation.oid,'UPDATE')))`},
		{"SECURITY DEFINER routine вне target, доступная owner", `SELECT EXISTS (
SELECT 1 FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname<>$1 AND namespace.nspname NOT IN ('pg_catalog','information_schema')
  AND left(namespace.nspname,length('pg_toast'))<>'pg_toast' AND left(namespace.nspname,length('pg_temp_'))<>'pg_temp_'
  AND procedure.prosecdef AND has_function_privilege(current_user,procedure.oid,'EXECUTE'))`},
		{"default ACL вне target", `SELECT EXISTS (
SELECT 1 FROM pg_default_acl defaults
LEFT JOIN pg_namespace namespace ON namespace.oid=defaults.defaclnamespace
CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl
WHERE defaults.defaclrole=(SELECT oid FROM pg_roles WHERE rolname=current_user)
  AND (defaults.defaclnamespace=0 OR namespace.nspname<>$1)
  AND acl.grantee<>(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
		{"default ACL в target", `SELECT EXISTS (
SELECT 1 FROM pg_default_acl defaults JOIN pg_namespace namespace ON namespace.oid=defaults.defaclnamespace
CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl
WHERE defaults.defaclrole=(SELECT oid FROM pg_roles WHERE rolname=current_user)
  AND namespace.nspname=$1 AND acl.grantee<>(SELECT oid FROM pg_roles WHERE rolname=current_user))`},
	}
	for _, check := range checks {
		var present bool
		var err error
		if strings.Contains(check.query, "$2") {
			err = query.QueryRow(ctx, check.query, targetSchema.String(), ownerRole).Scan(&present)
		} else if strings.Contains(check.query, "$1") {
			err = query.QueryRow(ctx, check.query, targetSchema.String()).Scan(&present)
		} else {
			err = query.QueryRow(ctx, check.query).Scan(&present)
		}
		if err != nil {
			return fmt.Errorf("migrate: authority: %s: %w", check.label, err)
		}
		if present {
			return fmt.Errorf("migrate: authority: %s; ApplyAs запрещён для схемы %q", check.label, targetSchema)
		}
	}
	return nil
}
