# Crawl4AI-плейбук: поиск контактов, которых нет на базовом эндпоинте

**Назначение.** Зафиксировать набор инструментов и приёмов Crawl4AI, решающих
задачу «контакт не на главной странице»: навигация по релевантности, слои
экстракции, OCR для email в картинках и границы автономности. Документ —
рабочий материал для change'а `mvp-lead-search` (`openspec/changes/mvp-lead-search/proposal.md`,
раздел «What Changes» — краулер). Писался по результатам ресёрча 2026-09-22;
источники — в конце.

Все механизмы ниже — штатные компоненты Crawl4AI v0.9.x и вызываются через его
API со стороны нашего Go-адаптера (сам Crawl4AI живёт в контейнере).

## 1. Навигационный слой (найти нужную страницу)

**`BestFirstCrawlingStrategy` + `KeywordRelevanceScorer` — основной инструмент.**
Ключевые слова (`contact`, `team`, `about`, `staff`, `impressum`), каждая
найденная ссылка оценивается по URL и анкорному тексту; обход идёт не по
глубине, а по релевантности — самые многообещающие страницы посещаются первыми.

```python
scorer = KeywordRelevanceScorer(
    keywords=["contact", "team", "staff", "about"], weight=0.8
)
strategy = BestFirstCrawlingStrategy(
    max_depth=3, url_scorer=scorer, max_pages=25
)
```

Подход уже используется в реальных пайплайнах (например, генерация датасета
документации по ключевым словам продукта) и переносится на поиск контактных
страниц без изменений.

**`AsyncUrlSeeder` — быстрый путь без обхода сайта.** Вытаскивает URL прямо из
sitemap.xml, фильтрует по паттерну (`*contact*`, `*team*`) и релевантности;
быстрее полноценного deep crawl на крупных сайтах.

**`FilterChain` + `URLPatternFilter`** дополняет скорер: отсекает заведомо
нерелевантные разделы (блог, новости, каталог товаров) до того, как краулер туда
пойдёт, экономя бюджет обхода.

## 2. Слой экстракции (вытащить контакт из найденной страницы)

**`LLMExtractionStrategy` + Pydantic-схема** (`name`, `email`, `phone`, `role`)
превращает неструктурированный текст вида «отдел продаж — Иван Петров» в JSON
независимо от вёрстки страницы.

**Для регулярных сайтов — сначала `JsonCssExtractionStrategy`** по типовым
паттернам (`mailto:`, `tel:`, класс `.contact-info`): дёшево, без LLM.
**LLM подключается только как fallback**, если селектор ничего не нашёл, —
та же экономика эскалации, что зафиксирована в proposal (слой 3 → 4).

## 3. Слой для нестандартных случаев (изображения, PDF)

Email, спрятанный в картинке (частый приём защиты от спам-ботов), стандартный
текстовый пайплайн Crawl4AI не видит. Нужен отдельный OCR-шаг: **pytesseract** с
предобработкой изображения (grayscale, повышение контраста, бинаризация) перед
распознаванием.

Архитектурно результат OCR подаётся в тот же regex/LLM-слой, что и обычный
текст страницы, — это просто ещё один источник текста перед экстракцией, а не
отдельная система. На нашей стороне — tesseract в образе контейнера
(правка Dockerfile-обёртки, отмечена в рисках proposal).

## 4. Что остаётся ручной работой (граница автономности)

- Ни один из механизмов **не кликает автономно** по кнопкам «Показать email» и
  не проходит формы поиска сотрудника. Такие случаи закрываются явным
  `js_code`/`wait_for` в `CrawlerRunConfig`, прописанным под структуру
  конкретного сайта.
- **Полностью автономная агентная навигация** (сама решает, куда кликнуть,
  чтобы найти контакт) в Crawl4AI не реализована — это уровень Firecrawl
  `/agent`. Если такая возможность нужна регулярно — отдельный пункт следующего
  среза, а не то, что собирается из существующих компонентов.

## Источники

- Deep Crawling — Crawl4AI Documentation (v0.9.x): https://docs.crawl4ai.com/core/deep-crawling/
- Crawl4AI Guide: Web Crawling for LLMs, RAG, and AI Agents (Scrapfly): https://scrapfly.io/blog/posts/crawl4AI-explained
- Crawl4AI Markdown scraper for documentation dataset generation (Zscaler): https://gist.github.com/norandom/85ece0c4bd30645e2d1fd4ff158d5686
- Setting Up Llm Extraction (decodo): https://decodo.com/blog/crawl4ai-tutorial
- URL Seeding — Crawl4AI Documentation (v0.9.x): https://docs.crawl4ai.com/core/url-seeding/
- Crawl4AI Complete SDK Documentation: https://docs.crawl4ai.com/complete-sdk-reference/
- Email-image-to-text-recognition (OCR-пайплайн, OpenCV + Tesseract): https://github.com/Dogherty/Email-image-to-text-recognition
- pytesseract (PyPI): https://pypi.org/project/pytesseract/
- Deep Crawling (DeepWiki, unclecode/crawl4ai): https://deepwiki.com/unclecode/crawl4ai/7.1-deep-crawling
- Advanced Patterns (DeepWiki, unclecode/crawl4ai): https://deepwiki.com/unclecode/crawl4ai/12.5-advanced-patterns
- DeepCrawlStrategy (Pocket Flow Tutorial): https://the-pocket.github.io/PocketFlow-Tutorial-Codebase-Knowledge/Crawl4AI/08_deepcrawlstrategy.html
