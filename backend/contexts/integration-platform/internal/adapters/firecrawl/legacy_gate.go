package firecrawl

import (
	"errors"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/integrationerror"
)

func legacyDisabledError() error {
	return errors.Join(budget.ErrLegacyExecutionDisabled, integrationerror.Error{
		Category:    integrationerror.CategoryTemporarilyUnavailable,
		AdapterName: "firecrawl", ContractName: "legacy-acquisition",
		Reason: "старый поиск отключён для общего учёта Firecrawl; используйте исследовательский запуск",
	})
}
