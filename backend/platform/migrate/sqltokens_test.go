package migrate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPilotMigrationCatalogPassesApplyAsSQLGate(t *testing.T) {
	targets := []struct {
		dir    string
		schema Schema
	}{
		{"../../contexts/request-fulfillment/migrations/green", Schema("request-fulfillment")},
		{"../../platform/consoleauth/migrations/green", Schema("consoleauth")},
		{"../../contexts/integration-platform/migrations", Schema("integration-platform")},
	}
	for _, target := range targets {
		t.Run(target.schema.String(), func(t *testing.T) {
			migrations, err := Load(filepath.Clean(target.dir), target.schema)
			if err != nil {
				t.Fatal("load migration catalog:", err)
			}
			if len(migrations) == 0 {
				t.Fatal("empty migration catalog")
			}
			for _, migration := range migrations {
				if _, err := removeOwnedSchemaBootstrap(migration.SQL, target.schema); err != nil {
					t.Errorf("migration %s rejected by ApplyAs safe SQL gate: %v", migration.Name, err)
				}
			}
		})
	}
}

func TestRemoveOwnedSchemaBootstrapUsesPostgresLexicalTokens(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{
			name: "quoted dashed schema",
			input: `-- leading comment
CREATE /* gap */ SCHEMA IF NOT EXISTS "request-fulfillment";
CREATE TABLE "request-fulfillment".t (v text);`,
			want: `-- leading comment

CREATE TABLE "request-fulfillment".t (v text);`,
		},
		{
			name: "schema words in comments and literals",
			input: `-- DROP SCHEMA unsafe;
SELECT 'CREATE SCHEMA unsafe;';
DO $proc$ BEGIN RAISE NOTICE 'ALTER SCHEMA'; END $proc$;`,
			want: `-- DROP SCHEMA unsafe;
SELECT 'CREATE SCHEMA unsafe;';
DO $proc$ BEGIN RAISE NOTICE 'ALTER SCHEMA'; END $proc$;`,
		},
		{
			name: "nested comments and escaped string semicolon",
			input: `/* outer /* CREATE SCHEMA x; */ end */
SELECT E'a\';DROP SCHEMA x;';
CREATE SCHEMA "request-fulfillment";`,
			want: `/* outer /* CREATE SCHEMA x; */ end */
SELECT E'a\';DROP SCHEMA x;';
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := removeOwnedSchemaBootstrap(test.input, Schema("request-fulfillment"))
			if err != nil {
				t.Fatal("removeOwnedSchemaBootstrap:", err)
			}
			if got != test.want {
				t.Fatalf("результат отличается\nполучено: %q\nожидалось: %q", got, test.want)
			}
		})
	}
}

func TestRemoveOwnedSchemaBootstrapRejectsOtherSchemaDDL(t *testing.T) {
	for _, sql := range []string{
		`CREATE SCHEMA other;`,
		`CREATE SCHEMA IF NOT EXISTS other;`,
		`ALTER SCHEMA "request-fulfillment" RENAME TO other;`,
		`DROP SCHEMA "request-fulfillment" CASCADE;`,
		`CREATE SCHEMA IF NOT EXISTS "request-fulfillment" AUTHORIZATION other;`,
	} {
		if _, err := removeOwnedSchemaBootstrap(sql, Schema("request-fulfillment")); err == nil {
			t.Errorf("schema DDL должен быть запрещён: %s", sql)
		}
	}
}

func TestMigrationSQLRejectsAllGrantAndRevokeStatements(t *testing.T) {
	for _, sql := range []string{
		`GRANT SELECT ON "request-fulfillment".request TO PUBLIC;`,
		`GRANT ALL ON "request-fulfillment".request TO hostile_login;`,
		`REVOKE SELECT ON "request-fulfillment".request FROM PUBLIC;`,
		`DO $$ BEGIN EXECUTE 'GRANT SELECT ON "request-fulfillment".request TO PUBLIC'; END $$;`,
		`DO $$ BEGIN EXECUTE 'REVOKE ALL ON "request-fulfillment".request FROM hostile_login'; END $$;`,
		`DO $$ BEGIN GRANT SELECT ON "request-fulfillment".request TO PUBLIC; END $$;`,
		`CREATE FUNCTION "request-fulfillment".unsafe_acl() RETURNS void LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'GRANT SELECT ON "request-fulfillment".request TO PUBLIC'; END $$;`,
		`CREATE PROCEDURE "request-fulfillment".unsafe_acl_proc() LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'REVOKE ALL ON "request-fulfillment".request FROM hostile_login'; END $$;`,
	} {
		if _, err := removeOwnedSchemaBootstrap(sql, Schema("request-fulfillment")); err == nil || !strings.Contains(err.Error(), "GRANT/REVOKE") {
			t.Errorf("migration privilege statement должен быть запрещён: sql=%s err=%v", sql, err)
		}
	}
}

func TestMigrationSQLChecksRoutineLanguageSecurityAndBodies(t *testing.T) {
	for _, sql := range []string{
		`CREATE FUNCTION "request-fulfillment".unsafe_definer() RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN NULL; END $$;`,
		`CREATE FUNCTION "request-fulfillment".unparsed() RETURNS void LANGUAGE c AS 'module', 'symbol';`,
		`CREATE PROCEDURE "request-fulfillment".dynamic_ddl() LANGUAGE plpgsql AS $$ BEGIN EXECUTE ddl_variable; END $$;`,
	} {
		if _, err := removeOwnedSchemaBootstrap(sql, Schema("request-fulfillment")); err == nil {
			t.Errorf("небезопасная routine definition должна быть отклонена: %s", sql)
		}
	}
	for _, sql := range []string{
		`CREATE FUNCTION "request-fulfillment".safe_body() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN -- quoted text is not SQL
RETURN 1; END $$;`,
		`CREATE FUNCTION "request-fulfillment".safe_dollar() RETURNS text LANGUAGE sql AS $body$ SELECT 'semi;colon' $body$;`,
		`CREATE FUNCTION "request-fulfillment".nested_dollar() RETURNS text LANGUAGE sql AS $outer$ SELECT $inner$ GRANT SELECT ON public.x TO PUBLIC; $inner$ $outer$;`,
	} {
		if _, err := removeOwnedSchemaBootstrap(sql, Schema("request-fulfillment")); err != nil {
			t.Errorf("безопасное routine body должно пройти: %v", err)
		}
	}
}

func TestTokenizeSQLRejectsUnterminatedLexicalUnits(t *testing.T) {
	for _, sql := range []string{`SELECT 'unterminated`, `SELECT "unterminated`, `/* no end`, `DO $body$ SELECT 1;`} {
		if _, err := tokenizeSQL(sql); err == nil || !strings.Contains(err.Error(), "незакрыт") {
			t.Errorf("ожидалась ошибка лексического элемента для %q; err=%v", sql, err)
		}
	}
}
