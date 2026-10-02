package query

import (
	"fmt"
	"strings"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// requireRef проверяет непустую ссылку прикладного запроса. Запросы приходят из
// HTTP-слоя с сырыми строками, поэтому форма проверяется здесь, до обращения к
// хранилищу.
func requireRef(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", domain.ErrEmptyID, field)
	}
	return nil
}
