package app

import (
	"fmt"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// Разделы консоли управления, доступ к которым решается на сервере.
//
// Раздел — понятие экрана, а не домена: консоль не вводит новых сущностей и не
// хранит роли локально. Матрица лишь сопоставляет раздел и действие с правом
// политики least-privilege (domain.Right), а роли актора приходят из
// контрактного снимка access-decision владельца — то есть из агрегата tenant.
type Section string

const (
	// SectionUsers — раздел «Пользователи и роли».
	SectionUsers Section = "users"
	// SectionIntegrations — раздел «Интеграции и адаптеры».
	SectionIntegrations Section = "integrations"
	// SectionTenant — раздел «Тенант» (в том числе переключение).
	SectionTenant Section = "tenant"
	// SectionAudit — раздел «Журнал аудита».
	SectionAudit Section = "audit"
	// SectionTopology — раздел «Топология сервисов» (только администратор).
	SectionTopology Section = "topology"
)

// Action — действие над разделом консоли.
type Action string

const (
	// ActionView — просмотр раздела.
	ActionView Action = "view"
	// ActionManage — изменение раздела (назначение и отзыв роли).
	ActionManage Action = "manage"
	// ActionExport — выгрузка данных раздела.
	ActionExport Action = "export"
	// ActionSwitch — переключение тенанта.
	ActionSwitch Action = "switch"
)

// sectionActionRights — матрица «раздел → действие → право». Действие, которого
// нет в матрице, запрещено (fail-closed): экран не получает доступ по умолчанию.
//
// Права берутся из объявленных доменом значений, поэтому расширение матрицы не
// вводит новых прав и не дублирует политику least-privilege.
var sectionActionRights = map[Section]map[Action]domain.Right{
	SectionUsers: {
		ActionView:   domain.RightManageRoles,
		ActionManage: domain.RightManageRoles,
	},
	SectionIntegrations: {
		ActionView: domain.RightManageTenants,
	},
	SectionTenant: {
		ActionView:   domain.RightManageTenants,
		ActionSwitch: domain.RightManageTenants,
	},
	SectionAudit: {
		ActionView:   domain.RightManageTenants,
		ActionExport: domain.RightManageTenants,
	},
	SectionTopology: {
		ActionView: domain.RightManageTenants,
	},
}

// RequiredRight возвращает право, которым определяется действие над разделом.
// Второе значение ложно, если пара «раздел → действие» не объявлена.
func RequiredRight(section Section, action Action) (domain.Right, bool) {
	actions, ok := sectionActionRights[section]
	if !ok {
		return "", false
	}
	right, ok := actions[action]
	return right, ok
}

// AuthorizeSection применяет матрицу доступа к набору ролей актора. Набор ролей
// берётся из снимка access-decision владельца; функция чистая — она не читает
// хранилище и не имеет побочных эффектов.
//
// Неизвестная пара «раздел → действие» и отсутствие нужного права дают
// ErrForbidden: решение принимается на сервере одинаково для всех экранов, а не
// условием в шаблоне.
func AuthorizeSection(roles domain.RoleSet, section Section, action Action) error {
	right, ok := RequiredRight(section, action)
	if !ok {
		return fmt.Errorf("%w: раздел %q, действие %q", ErrForbidden, section, action)
	}
	if !domain.RoleSetHasRight(roles, right) {
		return fmt.Errorf("%w: %s", ErrForbidden, right)
	}
	return nil
}
