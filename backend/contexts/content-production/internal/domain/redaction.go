package domain

import (
	"regexp"
	"strings"
)

// Исключение сведений о физических лицах из материалов, собранных с сайта
// клиента (capability landing-page-generator, требование «Извлечение содержания с
// сайта клиента»).
//
// Сведения о сотрудниках, руководстве и личные контакты не переносятся в
// наполнение и не сохраняются в контуре генератора. Общие контакты компании
// (почта отдела, городской телефон) сохраняются: они нужны наполнению.

// emailPattern выделяет адреса электронной почты.
var emailPattern = regexp.MustCompile(`[\p{L}\p{N}._%+-]+@[\p{L}\p{N}.-]+\.[\p{L}]{2,}`)

// mobilePhonePattern выделяет мобильные телефоны (личные): +7 9xx / 8 9xx.
var mobilePhonePattern = regexp.MustCompile(`(?:\+7|8)[\s-]?9\d{2}[\s-]?\d{3}[\s-]?\d{2}[\s-]?\d{2}`)

// roleWords — признаки строки о сотруднике или руководстве.
var roleWords = []string{
	"директор", "руководител", "основател", "сотрудник", "менеджер", "глава",
	"заместител", "бухгалтер", "юрист", "инженер", "специалист",
	"ceo", "cto", "cfo", "founder", "manager", "director", "owner",
	"г-н", "г-жа",
}

// generalMailboxes — общие адреса компании, не относящиеся к конкретному человеку.
var generalMailboxes = []string{
	"info", "sales", "support", "hello", "contact", "office", "hr", "admin",
	"mail", "team", "help", "billing", "partners",
}

// ExcludePersonalData исключает из собранного текста строки о сотрудниках и
// руководстве, а также личные адреса и мобильные телефоны. Возвращает очищенный
// текст и перечень исключённых фрагментов: факт исключения не скрывается.
func ExcludePersonalData(text string) (clean string, excluded []string) {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if containsRoleWord(trimmed) {
			excluded = append(excluded, trimmed)
			continue
		}
		redacted, removed := redactPersonalContacts(trimmed)
		excluded = append(excluded, removed...)
		if strings.TrimSpace(redacted) != "" {
			kept = append(kept, redacted)
		}
	}
	return strings.Join(kept, "\n"), excluded
}

// containsRoleWord сообщает, что строка описывает сотрудника или руководство.
func containsRoleWord(line string) bool {
	lower := strings.ToLower(line)
	for _, role := range roleWords {
		if strings.Contains(lower, role) {
			return true
		}
	}
	return false
}

// redactPersonalContacts удаляет из строки личные адреса и мобильные телефоны,
// сохраняя общие контакты компании. Возвращает очищенную строку и удалённые
// значения.
func redactPersonalContacts(line string) (string, []string) {
	var removed []string
	clean := line
	for _, email := range emailPattern.FindAllString(line, -1) {
		if !isPersonalEmail(email) {
			continue
		}
		removed = append(removed, email)
		clean = strings.ReplaceAll(clean, email, "")
	}
	for _, phone := range mobilePhonePattern.FindAllString(line, -1) {
		removed = append(removed, strings.TrimSpace(phone))
		clean = strings.ReplaceAll(clean, phone, "")
	}
	return strings.TrimSpace(clean), removed
}

// isPersonalEmail сообщает, что адрес принадлежит человеку, а не подразделению.
// Общие адреса компании (info, sales, support и т.п.) личными не считаются.
func isPersonalEmail(email string) bool {
	local, _, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	local = strings.ToLower(local)
	if index := strings.IndexByte(local, '+'); index >= 0 {
		local = local[:index]
	}
	for _, general := range generalMailboxes {
		if local == general {
			return false
		}
	}
	return true
}
