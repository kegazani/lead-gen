package domain

import "errors"

// Доменные ошибки контекста platform-governance.
//
// Ошибка описывает нарушение правила домена, а не сбой транспорта или хранилища.
// Вызывающий различает причину через errors.Is, поэтому ошибки объявлены
// именованными значениями, а не текстом.
var (
	// ErrEmptyID — идентификатор отсутствует или состоит из пробелов.
	ErrEmptyID = errors.New("домен: пустой идентификатор")

	// ErrMissingTime — событие домена без времени: его нельзя упорядочить.
	ErrMissingTime = errors.New("домен: отсутствует время")

	// ErrCompanyNameRequired — название компании пусто.
	ErrCompanyNameRequired = errors.New("домен: название компании обязательно")

	// ErrDescriptionRequired — описание компании пусто.
	ErrDescriptionRequired = errors.New("домен: описание компании обязательно")

	// ErrUserLoginRequired — логин пользователя пуст.
	ErrUserLoginRequired = errors.New("домен: логин пользователя обязателен")

	// ErrUserEmailRequired — email учётной записи пользователя пуст или неверен.
	ErrUserEmailRequired = errors.New("домен: email учётной записи обязателен")

	// ErrUserPasswordRequired — пароль пользователя не задан.
	ErrUserPasswordRequired = errors.New("домен: пароль пользователя обязателен")

	// ErrRolesRequired — у пользователя не задано ни одной роли.
	ErrRolesRequired = errors.New("домен: пользователю нужна хотя бы одна роль")

	// ErrUnknownRole — роль не объявлена доменом.
	ErrUnknownRole = errors.New("домен: неизвестная роль")

	// ErrDuplicateUser — пользователь уже принадлежит тенанту. Инвариант: роль
	// принадлежит ровно одному тенанту.
	ErrDuplicateUser = errors.New("домен: пользователь уже принадлежит тенанту")

	// ErrUserNotFound — пользователь отсутствует в составе тенанта. Роль
	// принадлежит ровно одному тенанту: назначить роль можно только его
	// пользователю.
	ErrUserNotFound = errors.New("домен: пользователь не найден в тенанте")

	// ErrRoleNotAssigned — попытка отозвать роль, которой у пользователя нет.
	ErrRoleNotAssigned = errors.New("домен: роль не назначена пользователю")

	// ErrTenantDisabled — отключённый тенант не принимает изменений доступа.
	ErrTenantDisabled = errors.New("домен: тенант отключён")

	// ErrUnknownAuditAction — действие аудита не объявлено доменом.
	ErrUnknownAuditAction = errors.New("домен: неизвестное действие аудита")

	// ErrUnknownAuditOutcome — результат аудита не объявлен доменом.
	ErrUnknownAuditOutcome = errors.New("домен: неизвестный результат аудита")

	// ErrAuditRecordInvalid — запись аудита некорректна.
	ErrAuditRecordInvalid = errors.New("домен: некорректная запись аудита")

	// ErrAuditRecordNotFound — записи аудита с таким идентификатором нет.
	ErrAuditRecordNotFound = errors.New("домен: запись аудита не найдена")

	// ErrInvalidSnapshot — сохранённое состояние тенанта внутренне противоречиво
	// и не может быть восстановлено без нарушения инвариантов.
	ErrInvalidSnapshot = errors.New("домен: некорректное сохранённое состояние тенанта")

	// ErrINNRequired — ИНН компании не задан.
	ErrINNRequired = errors.New("домен: ИНН компании обязателен")

	// ErrCompanyEmailRequired — email компании не задан или неверен.
	ErrCompanyEmailRequired = errors.New("домен: email компании обязателен")

	// ErrWebsiteRequired — ссылка на сайт компании не задана.
	ErrWebsiteRequired = errors.New("домен: ссылка на сайт компании обязательна")

	// ErrLegalDataRequired — юридические данные компании не заданы.
	ErrLegalDataRequired = errors.New("домен: юридические данные компании обязательны")

	// ErrDuplicateCompany — компания с такими идентифицирующими данными уже есть.
	ErrDuplicateCompany = errors.New("домен: компания с такими данными уже зарегистрирована")

	// ErrRegistrationState — действие регистрации недопустимо в текущем состоянии.
	ErrRegistrationState = errors.New("домен: действие недопустимо в текущем состоянии заявки на регистрацию")

	// ErrRegistrationReasonRequired — описание решения по регистрации пусто.
	ErrRegistrationReasonRequired = errors.New("домен: описание решения по регистрации обязательно")

	// ErrProfilePending — профиль на согласовании и не подлежит правке.
	ErrProfilePending = errors.New("домен: профиль на согласовании, правка запрещена")

	// ErrProfileIncomplete — профиль заполнен не полностью.
	ErrProfileIncomplete = errors.New("домен: профиль заполнен не полностью")

	// ErrProfileState — действие профиля недопустимо в текущем состоянии.
	ErrProfileState = errors.New("домен: действие недопустимо в текущем состоянии профиля")
)
