// Package integrationplatform объявляет опубликованные контракты портов
// интеграционной платформы: search, crawl, llm, mail и forum.
//
// Контракты выражены интерфейсами и DTO, а не конкретными пакетами владельца:
// потребитель (например, lead-management) держит только эти интерфейсы и не
// импортирует внутренние пакеты контекста-владельца. Реализации портов живут в
// модуле backend/contexts/integration-platform и впрыскиваются composition
// root'ом (cmd/lead-gen).
package integrationplatform
