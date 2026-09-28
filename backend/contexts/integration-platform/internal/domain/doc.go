// Package domain содержит доменную модель bounded context integration-platform:
// агрегаты adapter-registration и integration-invocation, их value objects,
// domain events и чистые политики adapter-selection и error-normalization.
//
// Пакет не импортирует app, ports, adapters, другие контексты и backend/platform.
// SDK поставщиков в домен не проникает: вызов идёт через порт, а ответы и ошибки
// поставщика нормализуются до доменных значений (design.md, Решения 1–3).
package domain
