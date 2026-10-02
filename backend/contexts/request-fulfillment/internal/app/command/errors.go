package command

import "errors"

// ErrTenantMismatch — заявка принадлежит другому тенанту. Сценарий не раскрывает
// существование чужой заявки: вызывающий получает отказ.
var ErrTenantMismatch = errors.New("сценарий: заявка принадлежит другому тенанту")
