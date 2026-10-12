## Why

Глобальная конфигурация OpenCode задаёт один набор моделей для всех агентов: координаторы, архитектор, исполнители и вспомогательные роли идут на DeepSeek. Пользователь хочет ролевую привязку GPT-моделей OpenAI (провайдер уже подключён по OAuth): архитектура и план — на Astra, координация — на Sol, код/ревью/суд — на Luna. Сейчас такой привязки нет ни в `~/.config/opencode/opencode.json`, ни в `~/.config/opencode/agents/*.md`.

## What Changes

- Глобальные coordinator-агенты `lead` и `lead-max`, а также дефолтная модель `model` → `openai/gpt-6.1-sol` с `variant: medium`.
- Глобальный `architect` → `openai/gpt-6-astra` с `variant: high`.
- Глобальные `coder`, `reviewer`, `judge` → `openai/gpt-6-luna` с `variant: high`.
- Встроенные агенты `plan` → `openai/gpt-6-astra` (`variant: high`) и `build` → `openai/gpt-6-luna` (`variant: high`) задаются в глобальном `agent`-блоке `opencode.json`.
- У агентов, перешедших на GPT, удалить `temperature` (у reasoning-моделей OpenAI он не поддерживается) и не переносить обратно.
- Вспомогательные роли `researcher`, `tester`, `writer`, `voter` → `openai/gpt-6-luna` с `variant: medium` (при высокой цене reasoning variant понижается до `low`).
- Глобальный `small_model` → `openai/gpt-6-luna`.
- План-гейт (plan gate): ID `openai/gpt-6-astra` и `openai/gpt-6.1-sol` есть в каталоге, но доступ (entitlement) не доказан. Перед записью этих ID — smoke-test. Если модель недоступна, её ID в глобальный target не пишется, change остаётся **частичным** и сообщает точную ошибку. Для `openai/gpt-6-luna` доступность подтверждена (стримилась), отдельного гейта нет.
- Честно зафиксировать: глобальный конфиг — **дефолт для проектов без локального override**. Project-local `config/.opencode` имеет более высокий приоритет и может переопределить модели; это не жёсткая гарантия.
- Целевые файлы лежат вне репозитория `lead-gen`: `~/.config/opencode/opencode.json` и `~/.config/opencode/agents/*.md`. В самом `lead-gen` конфига OpenCode нет.

## Capabilities

### New Capabilities

- Нет: изменение конфигурационное (tooling), поведение продукта не меняется. В `.openspec.yaml` установлен `skip_specs: true`; требование под конфиг не выдумывается.

### Modified Capabilities

- Нет.

## Impact

- **Домен не затронут:** bounded contexts, агрегаты, capability, domain events и межконтекстные контракты из `architecture/domain-catalog.yaml` не меняются; `docs/domain-model.md` и диаграммы не правятся. Change не создаёт доменных capability.
- **Файлы вне репозитория:** `~/.config/opencode/opencode.json` (`model`, `small_model`, `agent`-блок) и `~/.config/opencode/agents/{architect,lead,lead-max,coder,reviewer,judge,researcher,tester,writer,voter}.md` — цель применения. На OpenAI GPT-переходят все перечисленные агенты, включая четыре вспомогательные роли.
- **Репозиторий `lead-gen`:** только артефакты OpenSpec этого change; код, контракты, `deploy` не затрагиваются.
- **Риск приоритета:** локальный `opencode.json`/`.opencode` проекта перекрывает глобальные значения, поэтому результат — глобальный дефолт, а не гарантия для всех проектов.
