package domain

import (
	"fmt"
	"sort"
)

// RoleSet — value object «неизменяемый набор ролей пользователя» (role-set
// каталога).
//
// Инварианты:
//
//	V1 — набор содержит только роли, объявленные доменом;
//	V2 — набор неизменяем: With и Without возвращают новый набор, прежний не
//	     меняется;
//	V3 — порядок ролей канонический (сортировка по имени), повтор роли
//	     невозможен.
//
// Внутренний срез не выдаётся наружу и не меняется после построения, поэтому
// RoleSet можно безопасно копировать по значению.
type RoleSet struct {
	roles []Role
}

// NewRoleSet собирает набор ролей, проверяя инвариант V1. Нулевой набор ролей
// корректен и пуст.
func NewRoleSet(roles ...Role) (RoleSet, error) {
	unique := make(map[Role]struct{}, len(roles))
	for _, role := range roles {
		if !role.isValid() {
			return RoleSet{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
		}
		unique[role] = struct{}{}
	}
	ordered := make([]Role, 0, len(unique))
	for role := range unique {
		ordered = append(ordered, role)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if len(ordered) == 0 {
		return RoleSet{}, nil
	}
	return RoleSet{roles: ordered}, nil
}

// Roles возвращает копию ролей набора в каноническом порядке.
func (s RoleSet) Roles() []Role {
	return append([]Role(nil), s.roles...)
}

// Len возвращает число ролей в наборе.
func (s RoleSet) Len() int { return len(s.roles) }

// Empty сообщает, что набор не содержит ролей.
func (s RoleSet) Empty() bool { return len(s.roles) == 0 }

// Has сообщает, содержит ли набор роль.
func (s RoleSet) Has(role Role) bool {
	for _, current := range s.roles {
		if current == role {
			return true
		}
	}
	return false
}

// With возвращает новый набор, содержащий переданную роль. Прежний набор не
// меняется (инвариант V2). Добавление уже присутствующей роли возвращает
// эквивалентный набор.
func (s RoleSet) With(role Role) (RoleSet, error) {
	if !role.isValid() {
		return RoleSet{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	if s.Has(role) {
		return s, nil
	}
	roles := append(s.Roles(), role)
	return NewRoleSet(roles...)
}

// Without возвращает новый набор без переданной роли. Прежний набор не меняется
// (инвариант V2).
func (s RoleSet) Without(role Role) (RoleSet, error) {
	if !role.isValid() {
		return RoleSet{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	if !s.Has(role) {
		return s, nil
	}
	remaining := make([]Role, 0, len(s.roles))
	for _, current := range s.roles {
		if current != role {
			remaining = append(remaining, current)
		}
	}
	return NewRoleSet(remaining...)
}

// Equal сообщает равенство наборов по составу ролей.
func (s RoleSet) Equal(other RoleSet) bool {
	if len(s.roles) != len(other.roles) {
		return false
	}
	for i := range s.roles {
		if s.roles[i] != other.roles[i] {
			return false
		}
	}
	return true
}
