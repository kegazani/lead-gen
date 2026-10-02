package command

import "errors"

// ErrTenantMismatch — команда обращается к агрегату другого тенанта. Существование
// чужого агрегата этим отказом не раскрывается.
var ErrTenantMismatch = errors.New("команда: тенант не совпадает с тенантом агрегата")
