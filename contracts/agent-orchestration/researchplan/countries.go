package researchplan

import "strings"

// CountryCatalogVersion фиксирует коды ISO 3166-1 alpha-2. Локальные подписи и
// default-языки — ограниченный продуктовый профиль, не реестр интеграций.
const CountryCatalogVersion = "iso3166-1-profile/2026-10-15-v1"

type CountryProfile struct {
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	DefaultLanguages []string `json:"default_languages,omitempty"`
	Version          string   `json:"version"`
}

const countryCodes = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW"
const languageCodes = "aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu"

// LookupCountry не делает сетевых запросов и не обещает работоспособность
// поискового провайдера. Для остальных стран язык выбирается явно человеком.
func LookupCountry(code string) (CountryProfile, bool) {
	if len(code) != 2 || !strings.Contains(" "+countryCodes+" ", " "+code+" ") {
		return CountryProfile{}, false
	}
	p := CountryProfile{Code: code, Name: code, Version: CountryCatalogVersion}
	profiles := map[string]struct{ name, langs string }{
		"RU": {"Россия", "ru"}, "GB": {"Великобритания", "en"}, "US": {"США", "en"}, "DE": {"Германия", "de"}, "FR": {"Франция", "fr"},
		"AE": {"ОАЭ", "ar en"}, "CN": {"Китай", "zh"}, "IN": {"Индия", "en hi"}, "ES": {"Испания", "es"}, "IT": {"Италия", "it"},
		"PT": {"Португалия", "pt"}, "BR": {"Бразилия", "pt"}, "CA": {"Канада", "en fr"}, "AU": {"Австралия", "en"}, "NZ": {"Новая Зеландия", "en"},
		"JP": {"Япония", "ja"}, "KR": {"Южная Корея", "ko"}, "TR": {"Турция", "tr"}, "PL": {"Польша", "pl"}, "NL": {"Нидерланды", "nl"},
		"UA": {"Украина", "uk"}, "BY": {"Беларусь", "be ru"}, "KZ": {"Казахстан", "kk ru"}, "CH": {"Швейцария", "de fr it"}, "AT": {"Австрия", "de"},
	}
	if local, ok := profiles[code]; ok {
		p.Name = local.name
		p.DefaultLanguages = strings.Fields(local.langs)
	}
	return p, true
}

func ValidLanguage(code string) bool {
	return len(code) == 2 && strings.Contains(" "+languageCodes+" ", " "+code+" ")
}

// Countries возвращает независимый снимок для выбора рынка без платных вызовов.
func Countries() []CountryProfile {
	out := make([]CountryProfile, 0, 249)
	for _, code := range strings.Fields(countryCodes) {
		p, _ := LookupCountry(code)
		out = append(out, p)
	}
	return out
}
