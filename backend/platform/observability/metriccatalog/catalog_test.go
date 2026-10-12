package metriccatalog

import "testing"

func TestCatalogCopiesInputAndExposesSchemaCopies(t *testing.T) {
	schemas := []Schema{{Name: "calls_total", Kind: Counter, Labels: []string{"port", "adapter", "operation"}}}
	values := map[string][]string{
		"port": {"lead-search"}, "adapter": {"http", "smtp"}, "operation": {"search", "publish"},
	}
	tuples := []Tuple{{Labels: []string{"port", "adapter", "operation"}, Values: []string{"lead-search", "http", "search"}}}
	catalog := New(schemas, values, tuples)

	// Модификация исходных данных после сборки не должна расширять каталог.
	schemas[0].Labels[0] = "tenant_id"
	values["port"][0] = "tenant-123"
	tuples[0].Values[1] = "smtp"
	if !catalog.Validate("calls_total", Counter, map[string]string{"port": "lead-search", "adapter": "http", "operation": "search"}) {
		t.Fatal("изменение входных срезов повредило собранный каталог")
	}

	returned := catalog.Schemas()
	returned[0].Labels[0] = "user_id"
	if !catalog.Validate("calls_total", Counter, map[string]string{"port": "lead-search", "adapter": "http", "operation": "search"}) {
		t.Fatal("изменение возвращённой схемы повредило каталог")
	}
	if catalog.Validate("calls_total", Counter, map[string]string{"port": "lead-search", "adapter": "smtp", "operation": "publish"}) {
		t.Fatal("неразрешённый кортеж принят, хотя его значения разрешены по отдельности")
	}
}

func TestCatalogRequiresKnownMetricKindAndExactLabels(t *testing.T) {
	catalog := Default()
	cases := []struct {
		name   string
		kind   Kind
		labels map[string]string
	}{
		{"unknown metric", Counter, map[string]string{"service": "analytics"}},
		{"wrong kind", Histogram, map[string]string{"service": "analytics", "code": "200"}},
		{"missing label", Counter, map[string]string{"service": "analytics"}},
		{"extra label", Counter, map[string]string{"service": "analytics", "code": "200", "role": "worker"}},
	}
	for _, tc := range cases {
		if catalog.Validate(tc.name, tc.kind, tc.labels) {
			t.Errorf("некорректный образец принят: %s", tc.name)
		}
	}
}
