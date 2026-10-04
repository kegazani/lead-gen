package domain

import (
	"fmt"
	"time"
)

// Структура лендинга (capability landing-page-generator, требование «Структура
// лендинга»).
//
// Состав блоков определяется материалами клиента, а не задаётся заранее целиком:
// блок, для которого нет подтверждающих сведений, не создаётся и не заполняется
// вымышленными данными. Перечень показывается оператору до генерации текстов, а
// генерация запускается только после отдельного утверждения с фиксацией автора и
// времени.

// LandingBlock — блок структуры лендинга.
type LandingBlock struct {
	role LandingBlockRole
}

// NewLandingBlock собирает блок с объявленной доменом ролью.
func NewLandingBlock(role LandingBlockRole) (LandingBlock, error) {
	if !role.isValid() {
		return LandingBlock{}, fmt.Errorf("%w: %q", ErrUnknownBlockRole, role)
	}
	return LandingBlock{role: role}, nil
}

// Role возвращает роль блока.
func (b LandingBlock) Role() LandingBlockRole { return b.role }

// Label возвращает название роли на русском языке.
func (b LandingBlock) Label() string { return b.role.Label() }

// LandingStructure — перечень блоков и их порядок, утверждаемый оператором.
type LandingStructure struct {
	blocks     []LandingBlock
	approved   bool
	approvedBy ActorID
	approvedAt time.Time
}

// BuildLandingStructure строит перечень блоков по материалам. Блок включается,
// только если материалы дают для него подтверждение.
func BuildLandingStructure(materials LandingMaterials) (LandingStructure, error) {
	var blocks []LandingBlock
	for _, role := range LandingBlockRoles() {
		if !hasEvidence(materials, role) {
			continue
		}
		block, err := NewLandingBlock(role)
		if err != nil {
			return LandingStructure{}, err
		}
		blocks = append(blocks, block)
	}
	return LandingStructure{blocks: blocks}, nil
}

// Approve утверждает структуру как отдельное действие, фиксируя автора и время.
func (s *LandingStructure) Approve(actor ActorID, at time.Time) error {
	if err := actor.validate(); err != nil {
		return err
	}
	if at.IsZero() {
		return ErrMissingTime
	}
	s.approved = true
	s.approvedBy = actor
	s.approvedAt = at
	return nil
}

// EnsureApproved запрещает генерацию текстов до утверждения структуры.
func (s LandingStructure) EnsureApproved() error {
	if !s.approved {
		return ErrStructureNotApproved
	}
	return nil
}

// Blocks возвращает блоки в порядке показа.
func (s LandingStructure) Blocks() []LandingBlock {
	return append([]LandingBlock(nil), s.blocks...)
}

// Roles возвращает роли блоков в порядке показа.
func (s LandingStructure) Roles() []LandingBlockRole {
	out := make([]LandingBlockRole, 0, len(s.blocks))
	for _, block := range s.blocks {
		out = append(out, block.role)
	}
	return out
}

// IsEmpty сообщает, что структура не содержит блоков.
func (s LandingStructure) IsEmpty() bool { return len(s.blocks) == 0 }

// Approved сообщает, что структура утверждена оператором.
func (s LandingStructure) Approved() bool { return s.approved }

// ApprovedBy возвращает автора утверждения.
func (s LandingStructure) ApprovedBy() ActorID { return s.approvedBy }

// ApprovedAt возвращает время утверждения.
func (s LandingStructure) ApprovedAt() time.Time { return s.approvedAt }

// hasEvidence сообщает, что материалы подтверждают блок указанной роли.
func hasEvidence(materials LandingMaterials, role LandingBlockRole) bool {
	switch role {
	case LandingBlockProblem, LandingBlockOffer:
		return materials.BusinessDescription() != ""
	case LandingBlockSolution:
		return materials.ProductInfo() != ""
	case LandingBlockProof:
		return len(materials.Documents()) > 0
	case LandingBlockAction:
		return true
	default:
		return false
	}
}
