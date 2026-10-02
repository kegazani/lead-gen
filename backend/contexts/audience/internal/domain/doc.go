// Package domain содержит модель контекста audience: скоринг лида,
// сегментацию целевой аудитории и фиксацию неизменяемого снимка аудитории.
//
// Модель повторяет architecture/domain-catalog.yaml: агрегат audience-segment
// (root segment, entity lead-score, VO segment-criteria и score-value, событие
// segment-membership-changed, политика segment-eligibility) и агрегат
// audience-snapshot (root audience-snapshot, VO snapshot-criteria, событие
// audience-snapshot-created).
//
// Пакет не импортирует app, ports и adapters своего контекста, не знает о
// хранилище, брокере и транспорте, и не зависит от внешних модулей. Инварианты
// обеспечиваются агрегатами; события собираются внутри агрегата и вычитываются
// прикладным слоем (transactional outbox).
package domain
