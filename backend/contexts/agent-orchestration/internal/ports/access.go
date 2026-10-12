package ports

import "context"

// AccessChecker — проверка права актора действовать в тенанте. Форма совпадает с
// контрактом access-decision (владелец — platform-governance): потребитель
// механически сверяет запрос со снимком, а не воспроизводит RBAC.
//
// Ошибка означает отказ инфраструктуры проверить право (в том числе недоступность
// владельца или пустой субъект), а не отрицательное решение: вызывающий SHALL NOT
// трактовать ошибку как разрешение.
type AccessChecker interface {
	// CanAct сообщает, вправе ли актор действовать в тенанте.
	CanAct(ctx context.Context, actor, tenant string) (bool, error)
}
