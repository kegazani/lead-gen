// Package integrationplatform — корневой пакет сборки контекста
// integration-platform и библиотека его адаптеров.
//
// Каталог с дефисом (contexts/integration-platform) отвечает идентификатору
// контекста и сервиса из architecture/domain-catalog.yaml; имя Go-пакета должно
// быть валидным идентификатором, отсюда integrationplatform.
//
// Пакет собирает вертикальный срез контекста: доменные агрегаты
// adapter-registration и integration-invocation, use cases RegisterAdapter,
// SelectAdapter и InvokeIntegration, порты ProviderPort и репозитории, in-memory
// и тестовые адаптеры. Здесь же публикуются конструкторы адаптеров,
// реализующих опубликованные контракты search/crawl/llm/mail/forum (firecrawl,
// openai, forum, smtp) — их впрыскивает composition root cmd/lead-gen.
//
// Это единственное место межконтекстной сборки своего сервиса; бизнес-правил
// здесь нет. Публикуемые контракты контекста (provider-port, adapter-capability,
// integration-error) вынесены в модуль lead-gen/contracts/integration-platform.
package integrationplatform
