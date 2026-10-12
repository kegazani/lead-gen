package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const postgresIdentifierLimit = 63

var scopeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// RuntimeRoleName вычисляет имя LOGIN role из полного target/generation scope.
// JSON struct encoding даёт детерминированный порядок полей; в имени нет
// исходных значений scope, а 160-битный суффикс оставляет большой запас до
// вероятности коллизии при практическом числе deployment identities.
func RuntimeRoleName(scope RuntimeRoleScope) (string, error) {
	values := []string{scope.Environment, scope.Deployment, scope.Target, scope.Generation}
	for _, value := range values {
		if !scopeTokenPattern.MatchString(value) {
			return "", errors.New("migrate: некорректный runtime role scope")
		}
	}
	canonical, err := json.Marshal(struct {
		Environment string `json:"environment"`
		Deployment  string `json:"deployment_id"`
		Target      string `json:"target"`
		Generation  string `json:"generation"`
	}{scope.Environment, scope.Deployment, scope.Target, scope.Generation})
	if err != nil {
		return "", errors.New("migrate: не удалось канонизировать runtime role scope")
	}
	digest := sha256.Sum256(canonical)
	role := "leadgen_rt_" + hex.EncodeToString(digest[:20])
	if len(role) > postgresIdentifierLimit {
		return "", fmt.Errorf("migrate: runtime role identifier превышает %d байта", postgresIdentifierLimit)
	}
	return role, nil
}

func validateRuntimeRoleScope(spec RoleSet) error {
	if spec.RuntimeScope == nil {
		return nil
	}
	expected, err := RuntimeRoleName(*spec.RuntimeScope)
	if err != nil {
		return err
	}
	if expected != spec.RuntimeRole {
		return errors.New("runtime role не соответствует target/generation scope")
	}
	if spec.RuntimeScope.Target != spec.Schema.String() {
		return errors.New("runtime role target не соответствует managed schema")
	}
	return nil
}
