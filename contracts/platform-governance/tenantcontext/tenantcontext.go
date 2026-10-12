// Package tenantcontext объявляет обязательный контекст тенанта для команд и сообщений.
package tenantcontext

import (
	"errors"
	"strings"
)

// Context — изоляционный scope команды или сообщения.
type Context struct {
	TenantID string `json:"tenant_id"`
}

// Validate отвергает пустой идентификатор, не раскрывая переданное значение.
func (c Context) Validate() error {
	if strings.TrimSpace(c.TenantID) == "" {
		return errors.New("tenantcontext: отсутствует обязательное поле tenant_id")
	}
	return nil
}
