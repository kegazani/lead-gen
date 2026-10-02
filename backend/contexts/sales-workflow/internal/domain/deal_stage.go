package domain

import "fmt"

// DealStage — VO deal-stage: состояние сделки из согласованного жизненного цикла
// (architecture/domain-catalog.yaml, value_objects.deal-stage).
//
// Жизненный цикл:
//
//	lead → qualified → proposal → negotiation → won
//	   └────────┴───────────┴────────────┴──────→ lost
//
// won и lost терминальны: из них переходов нет. Переходы следуют согласованному
// workflow — инвариант агрегата deal.
type DealStage string

const (
	// StageLead — первичный контакт, сделка заведена.
	StageLead DealStage = "lead"

	// StageQualified — потребность и бюджет подтверждены.
	StageQualified DealStage = "qualified"

	// StageProposal — клиенту отправлено предложение.
	StageProposal DealStage = "proposal"

	// StageNegotiation — условия согласовываются.
	StageNegotiation DealStage = "negotiation"

	// StageWon — сделка выиграна (терминальный этап).
	StageWon DealStage = "won"

	// StageLost — сделка проиграна (терминальный этап).
	StageLost DealStage = "lost"
)

// DealStages возвращает согласованный набор этапов сделки.
func DealStages() []DealStage {
	return []DealStage{
		StageLead, StageQualified, StageProposal,
		StageNegotiation, StageWon, StageLost,
	}
}

// IsValid сообщает, что этап объявлен доменом.
func (s DealStage) IsValid() bool {
	switch s {
	case StageLead, StageQualified, StageProposal, StageNegotiation, StageWon, StageLost:
		return true
	default:
		return false
	}
}

// validate отвергает этап вне согласованного набора.
func (s DealStage) validate() error {
	if !s.IsValid() {
		return fmt.Errorf("%w: %q", ErrUnknownDealStage, s)
	}
	return nil
}

// IsTerminal сообщает, что этап терминален: из него нет переходов.
func (s DealStage) IsTerminal() bool {
	switch s {
	case StageWon, StageLost:
		return true
	default:
		return false
	}
}

// AllowsAttribution сообщает, что на этапе допустимо подтверждать результат.
// Атрибуция требует, чтобы предложение уже существовало (proposal и далее) и
// сделка не была проиграна.
func (s DealStage) AllowsAttribution() bool {
	switch s {
	case StageProposal, StageNegotiation, StageWon:
		return true
	default:
		return false
	}
}

// CanTransitionTo проверяет допустимость перехода по жизненному циклу:
//
//	lead        → qualified | lost
//	qualified   → proposal | lost
//	proposal    → negotiation | won | lost
//	negotiation → won | lost
//
// Из терминальных этапов переходов нет.
func (s DealStage) CanTransitionTo(to DealStage) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StageLead:
		return to == StageQualified || to == StageLost
	case StageQualified:
		return to == StageProposal || to == StageLost
	case StageProposal:
		return to == StageNegotiation || to == StageWon || to == StageLost
	case StageNegotiation:
		return to == StageWon || to == StageLost
	default:
		return false
	}
}
