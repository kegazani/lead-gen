package query

import (
	"context"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// ProposeLandingStructure — запрос перечня блоков лендинга по материалам
// клиента. Запрос не меняет состояние: он показывает структуру оператору до
// генерации текстов (требование «Состав блоков показывается до генерации
// текстов»).
type ProposeLandingStructure struct {
	SiteURL             string
	BusinessDescription string
	ProductInfo         string
	Documents           []string
}

// LandingBlockView — представление блока структуры.
type LandingBlockView struct {
	Role  string `json:"role"`
	Label string `json:"label"`
}

// LandingStructureView — представление предложенной структуры.
type LandingStructureView struct {
	Blocks []LandingBlockView `json:"blocks"`
	// Gaps — блоки, для которых материалов нет: они не создаются, а их нехватка
	// сообщается оператору.
	Gaps []LandingBlockView `json:"gaps,omitempty"`
}

// ProposeLandingStructureHandler — use case построения структуры по материалам.
type ProposeLandingStructureHandler struct{}

// NewProposeLandingStructureHandler собирает use case. Он не зависит от
// хранилища: структура строится из материалов и ничего не сохраняет.
func NewProposeLandingStructureHandler() *ProposeLandingStructureHandler {
	return &ProposeLandingStructureHandler{}
}

// Handle строит структуру и перечисляет блоки, которые материалами не
// подтверждены. Недостатка материалов для обязательного блока —
// ErrMaterialsInsufficient.
func (h *ProposeLandingStructureHandler) Handle(_ context.Context, q ProposeLandingStructure) (LandingStructureView, error) {
	materials, err := domain.NewLandingMaterials(q.SiteURL, q.BusinessDescription, q.ProductInfo, q.Documents)
	if err != nil {
		return LandingStructureView{}, err
	}
	structure, err := domain.BuildLandingStructure(materials)
	if err != nil {
		return LandingStructureView{}, err
	}

	view := LandingStructureView{Blocks: make([]LandingBlockView, 0, len(structure.Blocks()))}
	for _, block := range structure.Blocks() {
		view.Blocks = append(view.Blocks, LandingBlockView{Role: string(block.Role()), Label: block.Label()})
	}
	for _, role := range materials.Gaps() {
		block, err := domain.NewLandingBlock(role)
		if err != nil {
			return LandingStructureView{}, err
		}
		view.Gaps = append(view.Gaps, LandingBlockView{Role: string(block.Role()), Label: block.Label()})
	}
	return view, nil
}
