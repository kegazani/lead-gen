// Package researchmemory задаёт хранилище исследовательских данных красной зоны.
package researchmemory

import contract "lead-gen/contracts/lead-management/researchmemory"

type Repository interface {
	contract.Port
	contract.IdentityPort
}
