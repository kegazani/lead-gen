package domain

import (
	"fmt"
	"strings"
)

// Материалы клиента для генерации лендинга (capability landing-page-generator,
// требование «Приём материалов клиента»).
//
// Клиент даёт адрес сайта, рассказ о бизнесе, сведения о продукте или услуге и,
// при наличии, документы (коммерческое предложение). Обязательно только описание
// бизнеса: без сайта или документов наполнение всё равно строится по описанию.
// Недостающие сведения система не выдумывает, а называет (Gaps).

// LandingBlockRole — роль блока лендинга.
type LandingBlockRole string

const (
	// LandingBlockOffer — предложение.
	LandingBlockOffer LandingBlockRole = "offer"
	// LandingBlockProblem — проблема клиента.
	LandingBlockProblem LandingBlockRole = "problem"
	// LandingBlockSolution — решение и ценность.
	LandingBlockSolution LandingBlockRole = "solution"
	// LandingBlockProof — подтверждение.
	LandingBlockProof LandingBlockRole = "proof"
	// LandingBlockAction — призыв к действию с формой заявки.
	LandingBlockAction LandingBlockRole = "action"
)

// LandingBlockRoles возвращает согласованный порядок ролей блоков.
func LandingBlockRoles() []LandingBlockRole {
	return []LandingBlockRole{
		LandingBlockProblem,
		LandingBlockSolution,
		LandingBlockProof,
		LandingBlockOffer,
		LandingBlockAction,
	}
}

func (r LandingBlockRole) isValid() bool {
	switch r {
	case LandingBlockProblem, LandingBlockSolution, LandingBlockProof, LandingBlockOffer, LandingBlockAction:
		return true
	default:
		return false
	}
}

// Label возвращает название роли на русском языке.
func (r LandingBlockRole) Label() string {
	switch r {
	case LandingBlockOffer:
		return "Предложение"
	case LandingBlockProblem:
		return "Проблема клиента"
	case LandingBlockSolution:
		return "Решение и ценность"
	case LandingBlockProof:
		return "Подтверждение"
	case LandingBlockAction:
		return "Призыв к действию"
	default:
		return string(r)
	}
}

// LandingMaterials — материалы клиента. Неизменяемы.
type LandingMaterials struct {
	siteURL             string
	businessDescription string
	productInfo         string
	documents           []string
}

// NewLandingMaterials собирает материалы клиента. Обязательно только описание
// бизнеса: его отсутствие — нехватка материалов, о которой сообщается.
func NewLandingMaterials(siteURL, businessDescription, productInfo string, documents []string) (LandingMaterials, error) {
	description := strings.TrimSpace(businessDescription)
	if description == "" {
		return LandingMaterials{}, fmt.Errorf("%w: %s", ErrMaterialsInsufficient, LandingBlockOffer.Label())
	}
	clean := make([]string, 0, len(documents))
	for _, document := range documents {
		if trimmed := strings.TrimSpace(document); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return LandingMaterials{
		siteURL:             strings.TrimSpace(siteURL),
		businessDescription: description,
		productInfo:         strings.TrimSpace(productInfo),
		documents:           clean,
	}, nil
}

// SiteURL возвращает адрес сайта клиента (может быть пустым).
func (m LandingMaterials) SiteURL() string { return m.siteURL }

// BusinessDescription возвращает описание бизнеса (всегда непустое).
func (m LandingMaterials) BusinessDescription() string { return m.businessDescription }

// ProductInfo возвращает сведения о продукте или услуге (может быть пустым).
func (m LandingMaterials) ProductInfo() string { return m.productInfo }

// Documents возвращает предоставленные документы в порядке добавления.
func (m LandingMaterials) Documents() []string {
	return append([]string(nil), m.documents...)
}

// HasSite сообщает, что адрес сайта предоставлен.
func (m LandingMaterials) HasSite() bool { return m.siteURL != "" }

// Gaps возвращает сведения, которых не хватает для блоков, опирающихся на
// материалы, не выдумывая их. Пустой список — материалов достаточно для состава,
// определяемого ими.
func (m LandingMaterials) Gaps() []LandingBlockRole {
	var gaps []LandingBlockRole
	if m.productInfo == "" {
		gaps = append(gaps, LandingBlockSolution)
	}
	if len(m.documents) == 0 {
		gaps = append(gaps, LandingBlockProof)
	}
	return gaps
}
