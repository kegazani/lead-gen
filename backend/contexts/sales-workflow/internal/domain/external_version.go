package domain

import (
	"fmt"
	"strings"
)

// ExternalVersion — VO external-version: версия записи сделки во внешней CRM, с
// которой сверяется синхронизация (architecture/domain-catalog.yaml,
// value_objects.external-version).
//
// Версия состоит из двух частей: непрозрачной ссылки на запись CRM (ref) и
// монотонного номера ревизии (revision). Сверять можно только версии одной и той
// же записи: разные ref означают разные записи и для агрегата crm-sync являются
// конфликтом, а не рассинхронизацией версий.
type ExternalVersion struct {
	ref      ExternalDealRef
	revision int64
}

// ExternalVersionSnapshot — сохранённое состояние VO external-version.
type ExternalVersionSnapshot struct {
	Ref      ExternalDealRef
	Revision int64
}

// NewExternalVersion создаёт внешнюю версию. Ссылка обязана быть непустой, а
// ревизия — положительной.
func NewExternalVersion(ref ExternalDealRef, revision int64) (ExternalVersion, error) {
	if strings.TrimSpace(string(ref)) == "" {
		return ExternalVersion{}, fmt.Errorf("%w: external_ref", ErrInvalidExternalVersion)
	}
	if revision < 1 {
		return ExternalVersion{}, fmt.Errorf("%w: revision %d", ErrInvalidExternalVersion, revision)
	}
	return ExternalVersion{ref: ref, revision: revision}, nil
}

// Ref возвращает ссылку на запись во внешней CRM.
func (v ExternalVersion) Ref() ExternalDealRef { return v.ref }

// Revision возвращает номер ревизии записи во внешней CRM.
func (v ExternalVersion) Revision() int64 { return v.revision }

// IsZero сообщает, что версия не установлена (сделка ещё не синхронизирована).
func (v ExternalVersion) IsZero() bool { return v.ref == "" && v.revision == 0 }

// SameRef сообщает, что версии относятся к одной записи внешней CRM.
func (v ExternalVersion) SameRef(other ExternalVersion) bool { return v.ref == other.ref }

// IsNewerThan сообщает, что версия строго новее другой версии той же записи.
func (v ExternalVersion) IsNewerThan(other ExternalVersion) bool {
	if v.ref != other.ref {
		return false
	}
	return v.revision > other.revision
}

// Follows сообщает, что версия — непосредственно следующая за другой версией той
// же записи (инкремент ревизии без пропуска).
func (v ExternalVersion) Follows(other ExternalVersion) bool {
	if v.ref != other.ref {
		return false
	}
	return v.revision == other.revision+1
}

// Snapshot возвращает состояние VO для сохранения.
func (v ExternalVersion) Snapshot() ExternalVersionSnapshot {
	return ExternalVersionSnapshot{Ref: v.ref, Revision: v.revision}
}

// rehydrateExternalVersion восстанавливает VO из сохранённого состояния. Нулевая
// пара (пустая ссылка, нулевая ревизия) допустима: сделка ещё не синхронизирована.
func rehydrateExternalVersion(s ExternalVersionSnapshot) (ExternalVersion, error) {
	if s.Ref == "" && s.Revision == 0 {
		return ExternalVersion{}, nil
	}
	return NewExternalVersion(s.Ref, s.Revision)
}
