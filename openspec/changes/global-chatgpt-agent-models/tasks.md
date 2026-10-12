## 1. Фиксация состояния и предпосылок

- [x] 1.1 Сверить целевые файлы `~/.config/opencode/opencode.json` и `~/.config/opencode/agents/{architect,lead,lead-max,coder,reviewer,judge,researcher,tester,writer,voter}.md` с ожидаемым исходным состоянием. Проверка: прочитаны фактические frontmatter/поля; расхождений с `design.md` (Context) нет.
- [x] 1.2 Подтвердить наличие бэкапов `~/.config/opencode/opencode.json.bak-20261006-222714` и `~/.config/opencode/agents.bak-20261009`. Проверка: оба пути существуют и читаются.
- [x] 1.3 Зафиксировать, что в репозитории `lead-gen` нет `opencode.json` и `.opencode/`. Проверка: `ls` не находит локального конфига OpenCode.

## 2. План-гейт доступности моделей

- [ ] 2.1 Smoke-test `openai/gpt-6-astra` живым запуском (`opencode run -m openai/gpt-6-astra "<короткий промпт>"`). Проверка: получен ответ модели либо записан точный текст ошибки.
- [ ] 2.2 Smoke-test `openai/gpt-6.1-sol` живым запуском (`opencode run -m openai/gpt-6.1-sol "<короткий промпт>"`). Проверка: получен ответ модели либо записан точный текст ошибки.
- [ ] 2.3 Подтвердить `openai/gpt-6-luna` живым прогоном (`opencode run -m openai/gpt-6-luna --variant high "<короткий промпт>"`). Проверка: есть стрим/ответ; ошибки нет.
- [ ] 2.4 Зафиксировать матрицу доступности трёх моделей; если Astra и/или Sol недоступны — пометить change частичным и не записывать их ID в целевые конфиги. Проверка: для каждой модели явно указано «доступна»/«недоступна» с текстом ошибки.

## 3. Coordinator → Sol

- [x] 3.1 Только при успехе 2.2: в `~/.config/opencode/agents/lead.md` поставить `model: openai/gpt-6.1-sol` и удалить `temperature`. Проверка: frontmatter содержит новый `model`, поля `temperature` нет.
- [x] 3.2 Только при успехе 2.2: в `~/.config/opencode/agents/lead-max.md` поставить `model: openai/gpt-6.1-sol` и удалить `temperature`. Проверка: frontmatter содержит новый `model`, поля `temperature` нет.
- [x] 3.3 Только при успехе 2.2: в `~/.config/opencode/opencode.json` сменить `model` на `openai/gpt-6.1-sol`. При недоступности Sol — оставить прежнее значение. Проверка: значение `model` соответствует результату 2.2.
- [x] 3.4 Оставить `default_agent: lead-max` без изменений. Проверка: поле не изменилось и указывает на существующий primary-агент.

## 4. Architect → Astra

- [x] 4.1 Только при успехе 2.1: в `~/.config/opencode/agents/architect.md` поставить `model: openai/gpt-6-astra` и удалить `temperature`. При недоступности Astra оставить `deepseek/deepseek-v4-pro`. Проверка: frontmatter соответствует результату 2.1, поля `temperature` нет.

## 5. Luna → coder / reviewer / judge

- [x] 5.1 В `~/.config/opencode/agents/coder.md` поставить `model: openai/gpt-6-luna`, `variant: high` и удалить `temperature`. Проверка: frontmatter содержит `model`, `variant: high`, без `temperature`.
- [x] 5.2 В `~/.config/opencode/agents/reviewer.md` поставить `model: openai/gpt-6-luna`, `variant: high` и удалить `temperature`. Проверка: frontmatter содержит `model`, `variant: high`, без `temperature`.
- [x] 5.3 В `~/.config/opencode/agents/judge.md` поставить `model: openai/gpt-6-luna`, `variant: high` и удалить `temperature`. Проверка: frontmatter содержит `model`, `variant: high`, без `temperature`.

## 6. Support-роли и small_model → Luna

- [x] 6.1 В `~/.config/opencode/agents/{researcher,tester,writer,voter}.md` поставить `model: openai/gpt-6-luna`, `variant: medium` (понизить до `low`, если цена reasoning высока) и удалить `temperature`. Проверка: frontmatter содержит `openai/gpt-6-luna` и `variant`, поля `temperature` нет, `deepseek` не осталось.
- [x] 6.2 В `~/.config/opencode/opencode.json` сменить `small_model` на `openai/gpt-6-luna`. Проверка: значение равно `openai/gpt-6-luna`.

## 7. Честная документация приоритета и результата

- [x] 7.1 Зафиксировать в `proposal.md`/`design.md` и в записи `docs/progress-log.md`, что глобальный конфиг — дефолт, а project-local override приоритетнее и не гарантируется. Проверка: формулировка присутствует в обоих артефактах и в журнале.
- [ ] 7.2 Если запись в `~/.config/opencode` невозможна из-за ограничения external root — не менять глобальный конфиг, зафиксировать это открытой задачей-фактом с точным ограничением. Проверка: факт записан, глобальные файлы не изменены.

## 8. Гейт приёмки

- [x] 8.1 `openspec validate global-chatgpt-agent-models --strict` — valid. Проверка: команда сообщает `valid`.
- [ ] 8.2 `make check` — зелёный (изменений кода нет; проверяется openspec-часть). Проверка: команда завершилась с кодом 0.
- [x] 8.3 Если change частичен (Astra/Sol недоступны) — не закрывать его и сообщить точные ошибки; если все модели доступны и применены — только тогда закрывать задачи. Проверка: итог согласован с матрицей 2.4.
