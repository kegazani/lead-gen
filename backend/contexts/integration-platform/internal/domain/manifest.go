package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// manifestVersionRe — semver вида MAJOR.MINOR.PATCH с необязательными pre-release
// и build-суффиксами. Версия manifest версионируется по тем же правилам, что и
// версия схемы контракта.
var manifestVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// AdapterName — стабильное имя адаптера. Имя не является секретом и участвует в
// событиях и диагностике.
type AdapterName string

// ParseAdapterName разбирает имя адаптера. Окружающие пробелы не значимы.
func ParseAdapterName(name string) (AdapterName, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%w: adapter_name", ErrEmptyAdapterName)
	}
	return AdapterName(trimmed), nil
}

// String возвращает каноническое имя адаптера.
func (n AdapterName) String() string { return string(n) }

func (n AdapterName) validate() error {
	_, err := ParseAdapterName(string(n))
	return err
}

// ManifestVersion — value object manifest-version: версия manifest с
// объявленными возможностями адаптера. Неизменяем; создать значение можно только
// через ParseManifestVersion, поэтому недопустимая версия в домен не попадает.
type ManifestVersion string

// ParseManifestVersion разбирает версию manifest как semver. Версия проверяется
// строго: окружающие пробелы не допускаются.
func ParseManifestVersion(version string) (ManifestVersion, error) {
	if !manifestVersionRe.MatchString(version) {
		return "", fmt.Errorf("%w: %q", ErrInvalidManifestVersion, version)
	}
	return ManifestVersion(version), nil
}

// String возвращает каноническую версию manifest.
func (v ManifestVersion) String() string { return string(v) }

// IsZero сообщает, что версия не задана.
func (v ManifestVersion) IsZero() bool { return v == "" }

func (v ManifestVersion) validate() error {
	_, err := ParseManifestVersion(string(v))
	return err
}

// CapabilityName — имя возможности адаптера (контракт adapter-capability). Набор
// имён закрыт доменом: возможность нельзя объявить произвольной строкой.
type CapabilityName string

const (
	// CapabilitySearch — поиск по внешнему источнику.
	CapabilitySearch CapabilityName = "search"
	// CapabilityCrawl — обход и извлечение содержимого страниц.
	CapabilityCrawl CapabilityName = "crawl"
	// CapabilityEnrich — обогащение данными из внешнего источника.
	CapabilityEnrich CapabilityName = "enrich"
	// CapabilitySend — отправка сообщения через поставщика.
	CapabilitySend CapabilityName = "send"
	// CapabilityPublish — публикация материала на площадке.
	CapabilityPublish CapabilityName = "publish"
)

var knownCapabilities = map[CapabilityName]struct{}{
	CapabilitySearch:  {},
	CapabilityCrawl:   {},
	CapabilityEnrich:  {},
	CapabilitySend:    {},
	CapabilityPublish: {},
}

// KnownCapabilities возвращает объявленный набор возможностей в порядке
// объявления. Возвращённый срез — копия: закрытый набор нельзя испортить.
func KnownCapabilities() []CapabilityName {
	return []CapabilityName{
		CapabilitySearch,
		CapabilityCrawl,
		CapabilityEnrich,
		CapabilitySend,
		CapabilityPublish,
	}
}

// IsValidCapabilityName сообщает, объявлена ли возможность доменом.
func IsValidCapabilityName(name CapabilityName) bool {
	_, ok := knownCapabilities[name]
	return ok
}

// ParseCapabilityName разбирает имя возможности: регистр и окружающие пробелы не
// значимы, неизвестное имя отвергается.
func ParseCapabilityName(name string) (CapabilityName, error) {
	normalized := CapabilityName(strings.ToLower(strings.TrimSpace(name)))
	if !IsValidCapabilityName(normalized) {
		return "", fmt.Errorf("%w: %q", ErrUnknownCapability, name)
	}
	return normalized, nil
}

// Manifest — типизированный manifest адаптера: стабильное имя, версия manifest и
// набор объявленных возможностей. Без map[string]any: возможности — значения с
// именами, а не строки на доверии. Одновременно manifest — часть агрегата
// adapter-registration.
//
// Поля закрыты, значения неизменяемы: изменить manifest можно только созданием
// нового значения.
type Manifest struct {
	adapterName  AdapterName
	version      ManifestVersion
	capabilities []CapabilityName
}

// NewManifest собирает и проверяет manifest. Набор возможностей непуст, каждое
// имя объявлено доменом, повторы отвергаются.
func NewManifest(name AdapterName, version ManifestVersion, capabilities []CapabilityName) (Manifest, error) {
	manifest := Manifest{
		adapterName:  name,
		version:      version,
		capabilities: append([]CapabilityName(nil), capabilities...),
	}
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// AdapterName возвращает стабильное имя адаптера.
func (m Manifest) AdapterName() AdapterName { return m.adapterName }

// Version возвращает версию manifest.
func (m Manifest) Version() ManifestVersion { return m.version }

// Capabilities возвращает копию объявленного набора возможностей.
func (m Manifest) Capabilities() []CapabilityName {
	return append([]CapabilityName(nil), m.capabilities...)
}

// IsZero сообщает, что manifest не собран.
func (m Manifest) IsZero() bool {
	return m.adapterName == "" && m.version == "" && len(m.capabilities) == 0
}

// Declares сообщает, объявлена ли возможность в manifest.
func (m Manifest) Declares(capability CapabilityName) bool {
	for _, declared := range m.capabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

// Require проверяет объявленную возможность. Необъявленная возможность — ошибка
// с именем адаптера и возможностью, без секретов. Метод — носитель инварианта
// «возможности адаптера проверяются до вызова»: любой вызывающий путь обязан
// вызвать его перед обращением к поставщику.
func (m Manifest) Require(capability CapabilityName) error {
	if m.Declares(capability) {
		return nil
	}
	return fmt.Errorf("%w: адаптер %q, возможность %q", ErrCapabilityNotDeclared, m.adapterName, capability)
}

func (m Manifest) validate() error {
	if err := m.adapterName.validate(); err != nil {
		return err
	}
	if err := m.version.validate(); err != nil {
		return err
	}
	if len(m.capabilities) == 0 {
		return fmt.Errorf("%w: адаптер %q", ErrEmptyManifestCapabilities, m.adapterName)
	}
	seen := make(map[CapabilityName]struct{}, len(m.capabilities))
	for _, capability := range m.capabilities {
		if !IsValidCapabilityName(capability) {
			return fmt.Errorf("%w: %q у адаптера %q", ErrUnknownCapability, capability, m.adapterName)
		}
		if _, duplicate := seen[capability]; duplicate {
			return fmt.Errorf("%w: %q у адаптера %q", ErrDuplicateCapability, capability, m.adapterName)
		}
		seen[capability] = struct{}{}
	}
	return nil
}
