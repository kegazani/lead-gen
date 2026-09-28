package firecrawl

// Модель стоимости операций Firecrawl в кредитах. Источник тарифов —
// https://docs.firecrawl.dev/billing. Базовые стоимости первой версии:
// scrape и crawl — 1 кредит за страницу, map — 1 кредит за вызов, search —
// 2 кредита за каждые начатые 10 результатов. Опции с дополнительной
// стоимостью (извлечение JSON) в этой версии не используются.

// Operation — тип операции Firecrawl, для которой считается стоимость.
type Operation string

const (
	// OpScrape — скрейп одной страницы (markdown).
	OpScrape Operation = "scrape"
	// OpSearch — поиск по запросу с верхней границей числа результатов.
	OpSearch Operation = "search"
	// OpMap — карта сайта (список URL) за один вызов.
	OpMap Operation = "map"
	// OpCrawl — обход сайта с верхней границей числа страниц.
	OpCrawl Operation = "crawl"
)

// CostScrape — стоимость скрейпа одной страницы.
func CostScrape() int { return 1 }

// CostMap — стоимость вызова /map.
func CostMap() int { return 1 }

// CostCrawl — верхний предел стоимости обхода: 1 кредит за страницу.
// Параметр pages — верхняя граница числа страниц (limit).
func CostCrawl(pages int) int {
	if pages < 0 {
		pages = 0
	}
	return pages
}

// CostSearch — стоимость поиска: 2 кредита за каждые начатые 10 результатов.
// Параметр results — верхняя граница числа запрошенных результатов (limit).
func CostSearch(results int) int {
	if results < 0 {
		results = 0
	}
	return 2 * ((results + 9) / 10)
}
