// Package domain — модель контекста content-production: агрегат content-package
// (корень content-asset с сущностями content-version, landing-content, document)
// и агрегат render-job, их value objects, доменные события, инварианты и политика
// immutable-content-version.
//
// Состав объявлен каталогом доменной архитектуры
// (architecture/domain-catalog.yaml, contexts[content-production]):
//
//   - aggregates: content-package (content-asset, content-version,
//     landing-content, document), render-job;
//   - value_objects: content-locale, render-format;
//   - domain_events: content-version-created (content-package),
//     content-rendered (render-job);
//   - policies: immutable-content-version.
//
// Пакет не знает о хранилищах, транспорте и внешних движках рендеринга: их
// подключают адаптеры за портами (internal/ports). Рендеринг вынесен за порт
// Renderer — движок производства контента (linguistics, presentation-generator,
// landing-page-generator) является внешней системой.
package domain
