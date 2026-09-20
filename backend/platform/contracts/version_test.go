package contracts

import "testing"

func TestIsValidSemver(t *testing.T) {
	valid := []string{"0.0.0", "1.2.3", "10.20.30", "1.0.0-alpha.1", "1.0.0+build.7", "2.1.0-rc.1+sha"}
	for _, v := range valid {
		if !IsValidSemver(v) {
			t.Errorf("IsValidSemver(%q) = false, ожидалось true", v)
		}
	}
	invalid := []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "01.2.3", "1.2.x", "1..2", "-1.0.0"}
	for _, v := range invalid {
		if IsValidSemver(v) {
			t.Errorf("IsValidSemver(%q) = true, ожидалось false", v)
		}
	}
}

func TestParseVersion(t *testing.T) {
	v, err := ParseVersion("2.10.3")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if v.Major != 2 || v.Minor != 10 || v.Patch != 3 {
		t.Fatalf("разобрана неверно: %+v", v)
	}

	v, err = ParseVersion("1.4.0-rc.2+build")
	if err != nil {
		t.Fatalf("ParseVersion с суффиксами: %v", err)
	}
	if v.Major != 1 || v.Minor != 4 || v.Patch != 0 {
		t.Fatalf("суффиксы должны отбрасываться: %+v", v)
	}

	if _, err := ParseVersion("не-версия"); err == nil {
		t.Fatal("некорректная версия должна вернуть ошибку")
	}
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		provider, consumer string
		want               bool
	}{
		{"1.2.0", "1.2.0", true},
		{"1.2.0", "1.5.0", true},  // provider.MINOR <= consumer.MINOR
		{"1.5.0", "1.2.0", false}, // потребитель не знает более новой MINOR
		{"2.0.0", "1.9.9", false}, // смена MAJOR
		{"1.2.9", "1.2.0", true},  // PATCH на совместимость не влияет
		{"мусор", "1.0.0", false},
		{"1.0.0", "", false},
	}
	for _, c := range cases {
		if got := Compatible(c.provider, c.consumer); got != c.want {
			t.Errorf("Compatible(%q, %q) = %v, ожидалось %v", c.provider, c.consumer, got, c.want)
		}
	}
}

func TestVersionString(t *testing.T) {
	if got := (Version{Major: 3, Minor: 0, Patch: 1}).String(); got != "3.0.1" {
		t.Fatalf("Version.String() = %q", got)
	}
}
