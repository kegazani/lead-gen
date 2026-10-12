package migrate

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type sqlTokenKind uint8

const (
	sqlWord sqlTokenKind = iota
	sqlQuotedIdentifier
	sqlLiteral
	sqlSymbol
)

type sqlToken struct {
	kind       sqlTokenKind
	text       string
	escape     bool
	start, end int
}

type sqlStatement struct {
	tokens       []sqlToken
	start, end   int
	semicolonEnd int
}

// removeOwnedSchemaBootstrap tokenizes SQL before inspecting DDL. It strips only
// the exact CREATE SCHEMA [IF NOT EXISTS] <owned-name> statement needed by legacy
// migration files; it rejects direct CREATE/DROP/ALTER SCHEMA elsewhere. Comments,
// quoted identifiers, string literals and dollar-quoted bodies are lexical units,
// never text-scanned as SQL statements.
func removeOwnedSchemaBootstrap(sql string, owned Schema) (string, error) {
	statements, err := tokenizeSQL(sql)
	if err != nil {
		return "", fmt.Errorf("migrate: разбор SQL: %w", err)
	}
	var spans [][2]int
	for _, statement := range statements {
		tokens := statement.tokens
		if len(tokens) < 2 || tokens[0].kind != sqlWord || tokens[1].kind != sqlWord {
			continue
		}
		verb, object := strings.ToUpper(tokens[0].text), strings.ToUpper(tokens[1].text)
		if object != "SCHEMA" || (verb != "CREATE" && verb != "ALTER" && verb != "DROP") {
			continue
		}
		if verb != "CREATE" {
			return "", fmt.Errorf("migrate: migration не может выполнять %s SCHEMA", verb)
		}
		nameIndex := 2
		if len(tokens) >= 5 && wordIs(tokens, 2, "IF") && wordIs(tokens, 3, "NOT") && wordIs(tokens, 4, "EXISTS") {
			nameIndex = 5
		}
		if nameIndex >= len(tokens) || len(tokens) != nameIndex+1 {
			return "", errors.New("migrate: поддерживается только простой bootstrap собственной схемы без дополнительных clauses")
		}
		nameToken := tokens[nameIndex]
		if nameToken.kind != sqlWord && nameToken.kind != sqlQuotedIdentifier {
			return "", errors.New("migrate: имя bootstrap-схемы не является SQL identifier")
		}
		name := nameToken.text
		if nameToken.kind == sqlWord {
			name = strings.ToLower(name)
		}
		if name != owned.String() {
			return "", fmt.Errorf("migrate: migration пытается создать чужую схему %q", name)
		}
		spans = append(spans, [2]int{statement.start, statement.semicolonEnd})
	}
	cleaned := sql
	if len(spans) > 0 {
		var builder strings.Builder
		last := 0
		for _, span := range spans {
			builder.WriteString(sql[last:span[0]])
			last = span[1]
		}
		builder.WriteString(sql[last:])
		cleaned = builder.String()
	}
	if err := validateMigrationSQL(cleaned, owned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func validateMigrationSQL(sql string, owned Schema) error {
	statements, err := tokenizeSQL(sql)
	if err != nil {
		return fmt.Errorf("migrate: разбор migration SQL: %w", err)
	}
	for _, statement := range statements {
		tokens := statement.tokens
		if len(tokens) == 0 {
			continue
		}
		if wordIs(tokens, 0, "DO") {
			if len(tokens) != 2 || tokens[1].kind != sqlLiteral || tokens[1].text == "" {
				return errors.New("migrate: DO block содержит неподдерживаемую форму; отказ до исполнения")
			}
			if err := validatePLpgSQLBlock(tokens[1], owned); err != nil {
				return err
			}
			continue
		}
		if isRoutineDDL(tokens) {
			if err := validateRoutineDefinition(tokens, owned); err != nil {
				return err
			}
		}
		if wordIs(tokens, 0, "ALTER") && wordIs(tokens, 1, "DEFAULT") {
			return errors.New("migrate: миграция не может менять DEFAULT ACL; настройка выполняется только provisioning lifecycle")
		}
		if isPrivilegeStatement(tokens) {
			return errors.New("migrate: операторы GRANT/REVOKE запрещены в migration SQL")
		}
		if beginsDDL(tokens) {
			if err := validateDDLQualification(tokens, owned); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePLpgSQLBlock(body sqlToken, owned Schema) error {
	statements, err := tokenizeSQL(body.text)
	if err != nil {
		return fmt.Errorf("migrate: разбор procedural body: %w", err)
	}
	for _, statement := range statements {
		tokens := statement.tokens
		for i, token := range tokens {
			if isPrivilegeStatement(tokens[i:]) {
				return errors.New("migrate: операторы GRANT/REVOKE запрещены внутри procedural body")
			}
			if token.kind == sqlWord && strings.EqualFold(token.text, "EXECUTE") {
				if i+1 >= len(tokens) || tokens[i+1].kind != sqlLiteral || tokens[i+1].escape || i+2 != len(tokens) {
					return errors.New("migrate: DO EXECUTE должен быть одной неизменяемой обычной строкой; динамическое выражение запрещено")
				}
				if err := validateDynamicDDL(tokens[i+1].text, owned); err != nil {
					return fmt.Errorf("migrate: небезопасный EXECUTE в procedural body: %w", err)
				}
			}
		}
		if err := validateContainedDDL(tokens, owned); err != nil {
			return err
		}
	}
	return nil
}

func isRoutineDDL(tokens []sqlToken) bool {
	if !wordIs(tokens, 0, "CREATE") {
		return false
	}
	i := 1
	if wordIs(tokens, i, "OR") && wordIs(tokens, i+1, "REPLACE") {
		i += 2
	}
	return wordIs(tokens, i, "FUNCTION") || wordIs(tokens, i, "PROCEDURE")
}

func validateRoutineDefinition(tokens []sqlToken, owned Schema) error {
	if err := validateDDLQualification(tokens, owned); err != nil {
		return err
	}
	for i := 0; i+1 < len(tokens); i++ {
		if wordIs(tokens, i, "SECURITY") && wordIs(tokens, i+1, "DEFINER") {
			return errors.New("migrate: SECURITY DEFINER routine запрещена в migration SQL без отдельного reviewed allowlist")
		}
	}
	language := ""
	bodyIndex := -1
	for i := 0; i+1 < len(tokens); i++ {
		if wordIs(tokens, i, "LANGUAGE") && isSQLIdentifier(tokens[i+1]) {
			language = strings.ToLower(tokens[i+1].text)
		}
		if wordIs(tokens, i, "AS") {
			if bodyIndex >= 0 || i+1 >= len(tokens) || tokens[i+1].kind != sqlLiteral || tokens[i+1].escape {
				return errors.New("migrate: routine body должен иметь ровно один анализируемый string/dollar-quoted AS literal")
			}
			bodyIndex = i + 1
		}
	}
	if bodyIndex < 0 || (language != "sql" && language != "plpgsql") {
		return fmt.Errorf("migrate: routine language/body %q неподдерживается; отказ fail-closed", language)
	}
	body := tokens[bodyIndex]
	if body.text == "" {
		return errors.New("migrate: пустое routine body отклонено fail-closed")
	}
	statements, err := tokenizeSQL(body.text)
	if err != nil {
		return fmt.Errorf("migrate: lexical error routine body: %w", err)
	}
	if language == "plpgsql" {
		return validatePLpgSQLBlock(body, owned)
	}
	return validateDDLStatementTokens(statements, owned)
}

func validateDynamicDDL(sql string, owned Schema) error {
	statements, err := tokenizeSQL(sql)
	if err != nil {
		return fmt.Errorf("лексическая ошибка dynamic SQL: %w", err)
	}
	if len(statements) != 1 {
		return errors.New("dynamic SQL должен содержать ровно один statement")
	}
	if isPrivilegeStatement(statements[0].tokens) {
		return errors.New("migrate: dynamic GRANT/REVOKE запрещён в migration SQL")
	}
	if err := validateDDLStatementTokens(statements, owned); err != nil {
		return err
	}
	if !beginsDDL(statements[0].tokens) {
		return errors.New("DO EXECUTE разрешён только для фиксированного DDL managed schema")
	}
	return nil
}

func validateDDLStatementTokens(statements []sqlStatement, owned Schema) error {
	for _, statement := range statements {
		tokens := statement.tokens
		if len(tokens) == 0 {
			continue
		}
		if wordIs(tokens, 0, "ALTER") && wordIs(tokens, 1, "DEFAULT") {
			return errors.New("ALTER DEFAULT PRIVILEGES запрещён в миграции")
		}
		if isPrivilegeStatement(tokens) {
			return errors.New("migrate: операторы GRANT/REVOKE запрещены в routine body")
		}
		if beginsDDL(tokens) {
			if err := validateDDLQualification(tokens, owned); err != nil {
				return err
			}
		}
	}
	return nil
}

func beginsDDL(tokens []sqlToken) bool {
	if len(tokens) == 0 || tokens[0].kind != sqlWord {
		return false
	}
	switch strings.ToUpper(tokens[0].text) {
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COMMENT", "REFRESH", "REINDEX", "CLUSTER":
		return true
	default:
		return false
	}
}

func isPrivilegeStatement(tokens []sqlToken) bool {
	return wordIs(tokens, 0, "GRANT") || wordIs(tokens, 0, "REVOKE")
}

func validateContainedDDL(tokens []sqlToken, owned Schema) error {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].kind != sqlWord || tokens[i+1].kind != sqlWord {
			continue
		}
		verb, object := strings.ToUpper(tokens[i].text), strings.ToUpper(tokens[i+1].text)
		if (verb == "CREATE" || verb == "ALTER" || verb == "DROP" || verb == "TRUNCATE") &&
			(object == "TABLE" || object == "INDEX" || object == "VIEW" || object == "MATERIALIZED" || object == "SEQUENCE" || object == "TYPE" || object == "DOMAIN" || object == "FUNCTION" || object == "PROCEDURE" || object == "TRIGGER" || object == "SCHEMA") {
			if object == "SCHEMA" {
				return fmt.Errorf("migrate: DO block содержит %s SCHEMA DDL", verb)
			}
			if err := validateDDLQualification(tokens[i:], owned); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDDLQualification(tokens []sqlToken, owned Schema) error {
	if len(tokens) > 1 && wordIs(tokens, 0, "ALTER") && wordIs(tokens, 1, "DEFAULT") {
		return errors.New("ALTER DEFAULT PRIVILEGES запрещён в migration SQL")
	}
	foundTarget := false
	for i := 0; i+2 < len(tokens); i++ {
		if !isSQLIdentifier(tokens[i]) || tokens[i+1].kind != sqlSymbol || tokens[i+1].text != "." || !isSQLIdentifier(tokens[i+2]) {
			continue
		}
		name := tokens[i].text
		if tokens[i].kind == sqlWord {
			name = strings.ToLower(name)
		}
		if name != owned.String() {
			return fmt.Errorf("migration DDL ссылается на постороннюю схему %q", name)
		}
		foundTarget = true
	}
	if !foundTarget {
		return errors.New("migration DDL обязан явно квалифицировать объект целевой схемой")
	}
	return nil
}

func isSQLIdentifier(token sqlToken) bool {
	return token.kind == sqlWord || token.kind == sqlQuotedIdentifier
}

func wordIs(tokens []sqlToken, index int, expected string) bool {
	return index < len(tokens) && tokens[index].kind == sqlWord && strings.EqualFold(tokens[index].text, expected)
}

func tokenizeSQL(sql string) ([]sqlStatement, error) {
	statements := make([]sqlStatement, 0, 4)
	current := sqlStatement{start: -1, end: -1}
	appendToken := func(token sqlToken) {
		if current.start < 0 {
			current.start = token.start
		}
		current.end = token.end
		current.tokens = append(current.tokens, token)
	}
	finish := func(semicolonEnd int) {
		if len(current.tokens) > 0 {
			current.semicolonEnd = current.end
			if semicolonEnd > 0 {
				current.semicolonEnd = semicolonEnd
			}
			statements = append(statements, current)
		}
		current = sqlStatement{start: -1, end: -1}
	}

	for i := 0; i < len(sql); {
		c := sql[i]
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if c == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			i += 2
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			start := i
			i += 2
			depth := 1
			for i < len(sql) && depth > 0 {
				switch {
				case i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*':
					depth++
					i += 2
				case i+1 < len(sql) && sql[i] == '*' && sql[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("незакрытый block comment с позиции %d", start)
			}
			continue
		}
		if c == ';' {
			finish(i + 1)
			i++
			continue
		}
		if c == '\'' {
			start := i
			escape := start > 0 && (sql[start-1] == 'e' || sql[start-1] == 'E') && (start == 1 || !isIdentifierContinue(rune(sql[start-2])))
			i++
			var value strings.Builder
			closed := false
			for i < len(sql) {
				if escape && sql[i] == '\\' && i+1 < len(sql) {
					value.WriteByte(sql[i])
					value.WriteByte(sql[i+1])
					i += 2
					continue
				}
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						value.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				value.WriteByte(sql[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("незакрытый string literal с позиции %d", start)
			}
			appendToken(sqlToken{kind: sqlLiteral, text: value.String(), escape: escape, start: start, end: i})
			continue
		}
		if c == '"' {
			start := i
			i++
			var value strings.Builder
			closed := false
			for i < len(sql) {
				if sql[i] == '"' {
					if i+1 < len(sql) && sql[i+1] == '"' {
						value.WriteByte('"')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				value.WriteByte(sql[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("незакрытый quoted identifier с позиции %d", start)
			}
			appendToken(sqlToken{kind: sqlQuotedIdentifier, text: value.String(), start: start, end: i})
			continue
		}
		if c == '$' {
			if delimiter, ok := dollarDelimiter(sql, i); ok {
				start := i
				bodyStart := i + len(delimiter)
				relativeEnd := strings.Index(sql[bodyStart:], delimiter)
				if relativeEnd < 0 {
					return nil, fmt.Errorf("незакрытое dollar-quoted тело с позиции %d", start)
				}
				i = bodyStart + relativeEnd + len(delimiter)
				appendToken(sqlToken{kind: sqlLiteral, text: sql[bodyStart : bodyStart+relativeEnd], start: start, end: i})
				continue
			}
		}
		if isIdentifierStart(rune(c)) {
			start := i
			i++
			for i < len(sql) && isIdentifierContinue(rune(sql[i])) {
				i++
			}
			appendToken(sqlToken{kind: sqlWord, text: sql[start:i], start: start, end: i})
			continue
		}
		appendToken(sqlToken{kind: sqlSymbol, text: sql[i : i+1], start: i, end: i + 1})
		i++
	}
	finish(0)
	return statements, nil
}

func dollarDelimiter(sql string, start int) (string, bool) {
	if start+1 >= len(sql) {
		return "", false
	}
	for i := start + 1; i < len(sql); i++ {
		if sql[i] == '$' {
			return sql[start : i+1], true
		}
		if i == start+1 && !(sql[i] == '_' || unicode.IsLetter(rune(sql[i]))) {
			return "", false
		}
		if i > start+1 && !(sql[i] == '_' || unicode.IsLetter(rune(sql[i])) || unicode.IsDigit(rune(sql[i]))) {
			return "", false
		}
	}
	return "", false
}

func isIdentifierStart(r rune) bool    { return r == '_' || unicode.IsLetter(r) || r >= 0x80 }
func isIdentifierContinue(r rune) bool { return isIdentifierStart(r) || unicode.IsDigit(r) || r == '$' }
