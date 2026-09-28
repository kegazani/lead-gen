package domain

import (
	"errors"
	"testing"
)

func mustManifest(t *testing.T, name, version string, capabilities ...CapabilityName) Manifest {
	t.Helper()
	adapterName, err := ParseAdapterName(name)
	if err != nil {
		t.Fatalf("имя адаптера: %v", err)
	}
	manifestVersion, err := ParseManifestVersion(version)
	if err != nil {
		t.Fatalf("версия manifest: %v", err)
	}
	manifest, err := NewManifest(adapterName, manifestVersion, capabilities)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return manifest
}

// Полный manifest валиден и отвечает на объявленные возможности.
func TestManifest_Valid(t *testing.T) {
	manifest := mustManifest(t, "firecrawl", "1.2.0", CapabilitySearch, CapabilityCrawl)
	if manifest.AdapterName() != "firecrawl" {
		t.Fatalf("имя адаптера: %q", manifest.AdapterName())
	}
	if manifest.Version() != "1.2.0" {
		t.Fatalf("версия: %q", manifest.Version())
	}
	if !manifest.Declares(CapabilitySearch) {
		t.Fatal("search должен быть объявлен")
	}
	if manifest.Declares(CapabilitySend) {
		t.Fatal("send не объявлен")
	}
	if err := manifest.Require(CapabilityCrawl); err != nil {
		t.Fatalf("Require объявленной возможности: %v", err)
	}
	if err := manifest.Require(CapabilitySend); !errors.Is(err, ErrCapabilityNotDeclared) {
		t.Fatalf("Require необъявленной возможности: %v", err)
	}
}

// Manifest отвергает пустое имя, недопустимую версию, пустой набор и
// неизвестную возможность.
func TestManifest_RejectsInvalid(t *testing.T) {
	name, _ := ParseAdapterName("adapter")
	version, _ := ParseManifestVersion("1.0.0")

	cases := []struct {
		name         string
		manifestName AdapterName
		version      ManifestVersion
		capabilities []CapabilityName
		want         error
	}{
		{"пустое имя", "", version, []CapabilityName{CapabilitySearch}, ErrEmptyAdapterName},
		{"пустая версия", name, "", []CapabilityName{CapabilitySearch}, ErrInvalidManifestVersion},
		{"недопустимая версия", name, "1.0", []CapabilityName{CapabilitySearch}, ErrInvalidManifestVersion},
		{"пустой набор", name, version, nil, ErrEmptyManifestCapabilities},
		{"неизвестная возможность", name, version, []CapabilityName{"nope"}, ErrUnknownCapability},
		{"повтор возможности", name, version, []CapabilityName{CapabilitySearch, CapabilitySearch}, ErrDuplicateCapability},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewManifest(tc.manifestName, tc.version, tc.capabilities)
			if !errors.Is(err, tc.want) {
				t.Fatalf("получено %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// Возвращённый набор возможностей — копия: manifest неизменяем.
func TestManifest_CapabilitiesAreCopy(t *testing.T) {
	manifest := mustManifest(t, "adapter", "1.0.0", CapabilitySearch)
	capabilities := manifest.Capabilities()
	capabilities[0] = CapabilitySend
	if !manifest.Declares(CapabilitySearch) {
		t.Fatal("изменение возвращённого среза не должно влиять на manifest")
	}
}

func TestParseManifestVersion(t *testing.T) {
	valid := []string{"0.0.0", "1.0.0", "2.13.4", "1.0.0-rc.1", "1.2.3-rc.1+build.5"}
	for _, version := range valid {
		if _, err := ParseManifestVersion(version); err != nil {
			t.Errorf("%q должен быть валиден: %v", version, err)
		}
	}
	invalid := []string{"", "1", "1.0", "v1.0.0", "01.0.0", "1.0.0.0", " 1.0.0"}
	for _, version := range invalid {
		if _, err := ParseManifestVersion(version); err == nil {
			t.Errorf("%q не должен быть валиден", version)
		}
	}
}

func TestParseCapabilityName(t *testing.T) {
	capability, err := ParseCapabilityName("  SEARCH ")
	if err != nil {
		t.Fatalf("регистр и пробелы не значимы: %v", err)
	}
	if capability != CapabilitySearch {
		t.Fatalf("получено %q", capability)
	}
	if _, err := ParseCapabilityName("nope"); !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("неизвестная возможность: %v", err)
	}
}
