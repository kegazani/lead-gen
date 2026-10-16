# Готовность собственного acquisition, 2026-10-15

Change `intelligent-lead-research`, задачи 1.3–1.5, 2.*, 6.*, 7.* и 8.*.
Это проверка следующего пакета реализации, а не изменение обязательного объёма.

В production-коде нового пути есть Firecrawl Search/Fetch и SearXNG Search.
Crawlee/Crawl4AI runtime и собственный HTTP/browser/OCR адаптер пока отсутствуют.
SearXNG запущен из закреплённого официального образа: health и JSON API доступны,
но оба upstream engine получают `CONNECT 403` от действующего облачного proxy.
Разрешения в черновике настройки среды не доказывают доступность текущего egress.
Географический IP, DNS на стороне регионального выхода и запрет private targets
на уровне фактического соединения этим испытанием не подтверждены.

Рекомендация для испытания — Crawlee HTTP/Playwright с обязательным сравнением
Crawl4AI, тонкого HTTP/Playwright и Firecrawl. Это инженерная гипотеза, а не
окончательный выбор пользователя или результат измерений. Установленный
Chromium не означает установленный crawler runtime и изолированный browser.

| Шаг | Конкретная зависимость и результат |
|---|---|
| Согласование старых планов | Pending `self-hosted-crawler` содержит robots=false и собственное LLM-извлечение Crawl4AI. До активации согласовать это с действующей source/robots policy и единым model gateway; один action не должен скрыто запускать два извлечения. |
| Корпус и сравнение | Подготовить размеченный smoke и независимый holdout, закрепить версии/лицензии/weights. Сравнить unique company/contact yield, точные locators, latency/resources и наблюдаемые ошибки на одинаковых источниках. |
| Контроль исходящих соединений | Проверять фактический destination после DNS, redirects и browser subrequests; блокировать private/link-local/metadata targets. URL precheck не заменяет эту защиту. Tenant cookies, credentials и proxy sessions изолированы. |
| География | Хранить отдельно market, query language, requested region и measured egress. Проверить региональный выход настоящим маршрутом; текущий общий proxy не объявляется географическим. |
| Статический HTTP | Сохранять bounded исходный документ, DOM/JSON-LD и contact/legal sections с локаторами, content hash и версией извлечения. Раздел реквизитов не удаляется очисткой main text. |
| Очередь | Одна долговечная acquisition frontier у integration-platform; engine не создаёт независимую скрытую очередь. Domain/session concurrency, robots/rate/backoff, bytes/time/attempt caps и replay проверяются сбоями. Денежный ledger остаётся только у Firecrawl. |
| Браузер | Сначала конкретные Playwright recipes для JS contacts/reveal/pagination/public search, с записями действий и постусловий. Формы коммуникации не исполняются исследовательским browser режимом. |
| Документы | Text-PDF до OCR; затем trial Docling/PaddleOCR/Tesseract. Page/bbox/crop transforms и неоднозначные символы сохраняются до исходного artifact; качество OCR не подменяет принадлежность контакта. |
| Поставка | Реальные source/country cohorts и rollback rehearsal до переключения; старый Firecrawl путь остаётся доступным. Результат недоступного внешнего поиска не считается успешной пустой выдачей. |

На момент проверки свободно около 1,1 ГиБ в файловом слое `/workspace` и
13 ГиБ в `/tmp`; установка тяжёлых browser/OCR images требует подходящего
размещения. Существующие demo volumes и контейнеры не удалялись.

Порядок и полные критерии сохраняются в
[поисковом blueprint](../concepts/intelligent-lead-research/11-search-implementation-blueprint.md),
[task blueprints](../concepts/intelligent-lead-research/task-blueprints.json) и
[аудите покрытия](intelligent-research-task-coverage-2026-10-15.md).
