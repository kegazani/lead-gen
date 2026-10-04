package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Проверки сведений наполнения (capability landing-page-generator, требования
// «Тексты наполнения» и «Проверка на заимствование»).
//
// Тексты строятся только из материалов клиента: числа, сроки, гарантии и
// результаты, которых в материалах нет, в наполнение не попадают. Наполнение не
// должно и дословно повторять чужой текст. Обе проверки детерминированы и не
// обращаются к внешним системам: они сравнивают порождённый текст с уже
// имеющимися материалами и между источниками. Отсутствие сведений — не повод их
// выдумать: проверка сообщает о расхождении, а не выбирает значение сама.

// wordPattern выделяет слова и числа в Unicode: кириллица и латиница равноправны.
var wordPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// numberOnlyPattern выделяет числовые токены текста.
var numberOnlyPattern = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

// quantitativePattern выделяет количественные сведения с единицей измерения:
// «30%», «5 дней», «100 000 руб». Число без единицы (год в адресе, номер дома) не
// считается показателем и в сверку источников не попадает.
var quantitativePattern = regexp.MustCompile(
	`\d+(?:[.,]\d+)?\s*(?:%|₽|руб[\p{L}]*|\$|€|дн[\p{L}]*|недел[\p{L}]*|месяц[\p{L}]*|лет|год[\p{L}]*|час[\p{L}]*|мин[\p{L}]*|раз[\p{L}]*|шт|кг|км|м²|м2)`,
)

// sentenceDelimiters разбивает текст на фрагменты по завершающей пунктуации и
// переводам строк.
var sentenceDelimiters = regexp.MustCompile(`[.!?\n]+`)

// claimMarkers — слова-обещания, не выводимые из материалов: гарантии, скидки,
// окупаемость, результаты. Маркер, которого нет в материалах, считается
// несогласованным сведением.
var claimMarkers = []string{
	"гарант", "бесплат", "скидк", "окуп", "прибыл", "эконом", "результат", "срок",
}

// minMatchWords — минимальная длина дословного совпадения в словах. Короткие
// обороты (наименование, реквизиты, общеупотребительные термины) не считаются
// заимствованием: их совпадение неизбежно и законно.
const minMatchWords = 5

// UnsourcedClaims возвращает сведения, которые есть в тексте, но отсутствуют в
// материалах клиента: числовые показатели, сроки, гарантии и результаты.
// Пустой список — текст не выходит за материалы. Текст не правится: решение о
// недопустимости принимает вызывающий.
func UnsourcedClaims(text string, materials LandingMaterials) []string {
	corpus := materialsCorpus(materials)
	if strings.TrimSpace(corpus) == "" {
		return nil
	}

	seen := make(map[string]struct{})
	var claims []string
	add := func(claim string) {
		if _, ok := seen[claim]; ok {
			return
		}
		seen[claim] = struct{}{}
		claims = append(claims, claim)
	}

	for _, number := range numberTokens(text) {
		if !containsNumber(corpus, number) {
			add(number)
		}
	}
	lowerText := strings.ToLower(text)
	lowerCorpus := strings.ToLower(corpus)
	for _, marker := range claimMarkers {
		if strings.Contains(lowerText, marker) && !strings.Contains(lowerCorpus, marker) {
			add(marker)
		}
	}
	sort.Strings(claims)
	return claims
}

// DetectDiscrepancies сообщает о расхождениях между сведениями с сайта клиента и
// предоставленными документами. Сравниваются количественные показатели: значение,
// встречающееся только в одном источнике, — расхождение. Система не выбирает
// источник сама, а передаёт расхождение оператору.
func DetectDiscrepancies(siteText string, documents []string) []string {
	siteClaims := quantitativeClaims(siteText)
	documentClaims := quantitativeClaims(strings.Join(documents, "\n"))
	if len(siteClaims) == 0 && len(documentClaims) == 0 {
		return nil
	}

	siteOnly := difference(siteClaims, documentClaims)
	documentOnly := difference(documentClaims, siteClaims)

	var out []string
	for _, claim := range siteOnly {
		out = append(out, fmt.Sprintf("Показатель %q есть на сайте и отсутствует в документах", claim))
	}
	for _, claim := range documentOnly {
		out = append(out, fmt.Sprintf("Показатель %q есть в документах и отсутствует на сайте", claim))
	}
	sort.Strings(out)
	return out
}

// VerbatimMatches возвращает фрагменты текста, дословно совпадающие с источниками.
// Фрагмент, совпадающий с исключением (наименование компании, реквизиты,
// общеупотребительный термин), не возвращается: его совпадение неизбежно и
// законно. Возвращаются исходные (не нормализованные) фрагменты.
func VerbatimMatches(text string, sources, protected []string) []string {
	normalizedSources := make([]string, 0, len(sources))
	for _, source := range sources {
		if normalized := normalizeWords(source); normalized != "" {
			normalizedSources = append(normalizedSources, normalized)
		}
	}
	normalizedProtected := make([]string, 0, len(protected))
	for _, item := range protected {
		if normalized := normalizeWords(item); normalized != "" {
			normalizedProtected = append(normalizedProtected, normalized)
		}
	}

	seen := make(map[string]struct{})
	var matches []string
	for _, fragment := range sentenceDelimiters.Split(text, -1) {
		trimmed := strings.TrimSpace(fragment)
		normalized := normalizeWords(trimmed)
		if len(strings.Fields(normalized)) < minMatchWords {
			continue
		}
		if containedInAny(normalizedProtected, normalized) {
			continue
		}
		if !containedInAny(normalizedSources, normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		matches = append(matches, trimmed)
	}
	return matches
}

// materialsCorpus собирает текст материалов клиента для сверки.
func materialsCorpus(materials LandingMaterials) string {
	parts := []string{materials.SiteURL(), materials.BusinessDescription(), materials.ProductInfo()}
	parts = append(parts, materials.Documents()...)
	return strings.Join(parts, "\n")
}

// numberTokens возвращает числовые токены текста.
func numberTokens(text string) []string {
	return numberOnlyPattern.FindAllString(text, -1)
}

// containsNumber сообщает, что число встречается в тексте как отдельный токен
// (границы — не цифры), чтобы «30» не совпадало с «300».
func containsNumber(corpus, number string) bool {
	pattern := regexp.MustCompile(`(?:^|[^\d])` + regexp.QuoteMeta(number) + `(?:[^\d]|$)`)
	return pattern.MatchString(corpus)
}

// quantitativeClaims возвращает количественные показатели с единицей измерения.
func quantitativeClaims(text string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, match := range quantitativePattern.FindAllString(text, -1) {
		normalized := normalizeClaim(match)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

// normalizeClaim приводит показатель к нижнему регистру и единому разделителю,
// сохраняя единицу измерения («30%», «5 дней»).
func normalizeClaim(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(text)), " ")
}

// difference возвращает элементы left, которых нет в right.
func difference(left, right []string) []string {
	set := make(map[string]struct{}, len(right))
	for _, item := range right {
		set[item] = struct{}{}
	}
	var out []string
	for _, item := range left {
		if _, ok := set[item]; !ok {
			out = append(out, item)
		}
	}
	return out
}

// normalizeWords приводит текст к последовательности слов в нижнем регистре,
// разделённых одним пробелом. Регистр и пунктуация не влияют на совпадение.
func normalizeWords(text string) string {
	words := wordPattern.FindAllString(strings.ToLower(text), -1)
	return strings.Join(words, " ")
}

// containedInAny сообщает, что нормализованный фрагмент содержится хотя бы в одной
// из нормализованных строк.
func containedInAny(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, item := range haystack {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}
