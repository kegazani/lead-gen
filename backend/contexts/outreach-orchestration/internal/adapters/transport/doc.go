// Package transport — транспортные адаптеры границы контекста
// outreach-orchestration по объявленному в каталоге транспорту связей.
//
// Это тонкие обёртки над примитивами platform/transport:
//
//   - ApprovedActionPublisher публикует публикуемый контракт `approved-action`
//     владельцем на связь outreach-to-delivery (интеграция command, транспорт
//     sync/http-json). Он реализует relay.Sink, поэтому подключается после
//     transactional outbox без изменений;
//   - AudienceSnapshotClient запрашивает потребляемый контракт `audience-snapshot`
//     на связь audience-to-outreach (интеграция query, транспорт sync/http-json);
//   - EngagementEventSubscriber принимает потребляемый контракт
//     `engagement-event` на связь observation-to-outreach (интеграция event,
//     транспорт async/amqp);
//   - ProcessingAuthoritySubscriber принимает потребляемый контракт
//     `processing-authority` на связь governance-policy-for-outreach (интеграция
//     policy, транспорт async/amqp).
//
// Адаптеры не принимают доменных решений и не содержат бизнес-правил: они
// сериализуют и разбирают контрактные DTO, переносят конверт и делегируют
// обработку вызывающему. Презентация контракта — типы ports/contracts; типы
// владельца другого контекста сюда не импортируются, а домен не импортирует
// platform/transport (design.md, Решения 2, 3 и 10).
package transport
