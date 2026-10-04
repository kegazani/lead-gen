// Package contracttest содержит общие contract suite портов контекста
// content-production: репозиториев контент-пакетов и заданий рендеринга, а также
// unit-of-work. Suite запускается против любой реализации через фабрику и потому
// не зависит от адаптера: in-memory и PostgreSQL проверяются одним и тем же
// набором проверок (design.md изменения context-persistence, Решение 1 и 4).
//
// Пакет предназначен для использования из тестов адаптеров и импортирует только
// сам ports, его domain и журнал outbox платформы.
package contracttest
