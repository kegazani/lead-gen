// Package domain — домен контекста data-governance: зоны данных, основания
// обработки и правила подавления контактов (architecture/domain-catalog.yaml).
//
// Состав строго по каталогу: агрегат processing-governance с корневой сущностью
// processing-basis и сущностью suppression-entry, value objects processing-purpose
// и retention-period, события processing-authority-changed и contact-suppressed,
// политики data-zone-policy и suppression-policy.
//
// Пакет не зависит от app и adapters и не импортирует внешние SDK: агрегат
// инкапсулирует поля, проверяет инварианты в конструкторах и методах и возвращает
// domain events.
package domain
