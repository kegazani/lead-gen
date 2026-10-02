package domain

import (
	"fmt"
	"strings"
)

// TransitionLink — value object трекинг-перехода: непрозрачная ссылка, адрес
// назначения и владелец перехода. Ссылка — не адрес: адрес возвращает владелец,
// а получатель перехода видит именно адрес.
//
// Ссылка хранит тенант и компанию: учёт состоявшегося перехода обязан отнести
// его к тем же данным, что и событие, из которого ссылка зарегистрирована.
type TransitionLink struct {
	// Ref — непрозрачная ссылка перехода.
	Ref LinkRef
	// Target — адрес назначения.
	Target string
	// TenantID — тенант-владелец перехода.
	TenantID TenantID
	// CompanyID — компания, к которой отнесён переход.
	CompanyID CompanyID
}

// NewTransitionLink собирает ссылку перехода, проверяя инварианты: непустые
// ссылку и адрес, известный тенант и компанию.
func NewTransitionLink(ref LinkRef, target string, tenant TenantID, company CompanyID) (TransitionLink, error) {
	if err := ref.validate(); err != nil {
		return TransitionLink{}, err
	}
	if strings.TrimSpace(target) == "" {
		return TransitionLink{}, fmt.Errorf("%w: ссылка %s", ErrMissingTarget, ref)
	}
	if err := tenant.validate(); err != nil {
		return TransitionLink{}, err
	}
	if err := company.validate(); err != nil {
		return TransitionLink{}, err
	}
	return TransitionLink{
		Ref:       ref,
		Target:    strings.TrimSpace(target),
		TenantID:  tenant,
		CompanyID: company,
	}, nil
}

// String возвращает непрозрачную ссылку.
func (l TransitionLink) String() string { return string(l.Ref) }
