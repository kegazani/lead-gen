package domain

// Политика immutable-content-version (architecture/domain-catalog.yaml,
// policies.immutable-content-version).
//
// Политика связывает вход контракта content-request с событием
// content-version-created: производство контента публикует новую версию, а не
// правит существующую. Опубликованная версия неизменяема — новый текст создаёт
// новую версию, прежняя остаётся как есть.
//
// Политика выражена структурно: у ContentVersion нет мутаторов, поле versions
// контент-пакета доступно только на чтение, а PublishVersion лишь добавляет
// версию в конец. Функция ниже фиксирует это утверждение для тестов и читателя:
// расширение списка версий обязано сохранить все прежние версии без изменений.

// ImmutableContentVersionPolicy — имя политики из каталога доменной архитектуры.
const ImmutableContentVersionPolicy = "immutable-content-version"

// PreservesPublishedVersions сообщает, что расширенный набор версий сохранил
// каждую прежнюю версию без изменений и лишь дополнил её новыми. Проверка служит
// исполняемым выражением политики immutable-content-version.
func PreservesPublishedVersions(before, after []ContentVersion) bool {
	if len(after) < len(before) {
		return false
	}
	for index, version := range before {
		if !equalVersion(version, after[index]) {
			return false
		}
	}
	return true
}

// equalVersion сравнивает значимые поля версии.
func equalVersion(a, b ContentVersion) bool {
	return a.id == b.id &&
		a.revision == b.revision &&
		a.body == b.body &&
		a.locale == b.locale &&
		a.createdAt.Equal(b.createdAt) &&
		a.createdBy == b.createdBy
}
