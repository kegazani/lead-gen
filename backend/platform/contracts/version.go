package contracts

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией
// вида MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool {
	return semverRe.MatchString(version)
}

// Version — разобранная semver-версия контракта без pre-release и build-частей.
// Для маршрутизации и проверки совместимости значимы только числа: суффиксы не
// меняют совместимость формата обмена.
type Version struct {
	Major int
	Minor int
	Patch int
}

// String возвращает версию в каноническом виде MAJOR.MINOR.PATCH.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ParseVersion разбирает semver-версию в Version. Суффиксы pre-release и build
// допускаются и отбрасываются; нечисловая или неполная версия — ошибка.
func ParseVersion(s string) (Version, error) {
	if !IsValidSemver(s) {
		return Version{}, fmt.Errorf("contracts: некорректная semver-версия %q", s)
	}

	// IsValidSemver гарантирует, что до первого разделителя суффикса лежат
	// ровно три числовых компонента.
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("contracts: разбор MAJOR в версии %q: %w", s, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("contracts: разбор MINOR в версии %q: %w", s, err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("contracts: разбор PATCH в версии %q: %w", s, err)
	}
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// Compatible сообщает, совместимы ли версии provider (публикуемая) и consumer
// (поддерживаемая потребителем).
//
// Совместимо, если совпадает MAJOR и MINOR провайдера не превосходит MINOR
// потребителя (provider.MINOR <= consumer.MINOR): потребитель не знает полей,
// добавленных в более новой MINOR, и не должен читать её молча. Смена MAJOR
// несовместима по определению. Некорректная версия любой из сторон считается
// несовместимой, а не паникой.
func Compatible(provider, consumer string) bool {
	p, err := ParseVersion(provider)
	if err != nil {
		return false
	}
	c, err := ParseVersion(consumer)
	if err != nil {
		return false
	}
	return p.Major == c.Major && p.Minor <= c.Minor
}
