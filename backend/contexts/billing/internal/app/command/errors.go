package command

import "errors"

// ErrTenantMismatch — агрегат принадлежит другому тенанту. Сценарий не раскрывает
// существование чужого агрегата: вызывающий получает отказ.
var ErrTenantMismatch = errors.New("сценарий: агрегат принадлежит другому тенанту")
