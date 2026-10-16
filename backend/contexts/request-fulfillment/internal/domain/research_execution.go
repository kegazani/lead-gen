package domain

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const ExecutionModeLegacy = "legacy"
const ExecutionModeResearch = "research"

var ErrExecutionModeConflict = errors.New("заявка уже закреплена за другим способом исполнения; создайте новую заявку")
var ErrResearchSelectionConflict = errors.New("утверждённые параметры исследования не совпадают; создайте новую заявку")

// ResearchSelection — утверждённые человеком типизированные границы поручения.
// Здесь нет поисковых строк, содержимого источников и выводов модели.
type ResearchSelection struct {
	Quantity      int
	Unit          string
	OfferRevision int
	Markets       []ResearchMarket
}
type ResearchMarket struct {
	Country   string
	Languages []string
}

func (in ResearchSelection) Clone() ResearchSelection {
	out := in
	out.Markets = append([]ResearchMarket(nil), in.Markets...)
	for i := range out.Markets {
		out.Markets[i].Languages = append([]string(nil), out.Markets[i].Languages...)
	}
	return out
}
func (in ResearchSelection) Validate() error {
	if in.Quantity < 1 || in.OfferRevision < 1 || len(in.Markets) == 0 || len(in.Markets) > 32 {
		return ErrResearchSelectionConflict
	}
	switch in.Unit {
	case "company", "company-with-contact", "email-ready":
	default:
		return ErrResearchSelectionConflict
	}
	seen := map[string]bool{}
	for _, market := range in.Markets {
		if len(market.Country) != 2 || strings.ToUpper(market.Country) != market.Country || seen[market.Country] || len(market.Languages) == 0 || len(market.Languages) > 8 {
			return ErrResearchSelectionConflict
		}
		seen[market.Country] = true
		for _, language := range market.Languages {
			if len(language) != 2 || strings.ToLower(language) != language {
				return ErrResearchSelectionConflict
			}
		}
	}
	return nil
}
func (in ResearchSelection) Equal(other ResearchSelection) bool {
	normalize := func(value ResearchSelection) ResearchSelection {
		value = value.Clone()
		sort.Slice(value.Markets, func(i, j int) bool { return value.Markets[i].Country < value.Markets[j].Country })
		for i := range value.Markets {
			sort.Strings(value.Markets[i].Languages)
		}
		return value
	}
	return reflect.DeepEqual(normalize(in), normalize(other))
}
func (r *Request) ExecutionMode() string {
	if r.executionMode != "" {
		return r.executionMode
	}
	// Старый одобренный снимок не получает разрешение на второй новый контур.
	if r.validationDecision != nil && r.validationDecision.Approved() {
		return ExecutionModeLegacy
	}
	return ""
}
func (r *Request) ResearchSelection() *ResearchSelection {
	if r.researchSelection == nil {
		return nil
	}
	out := r.researchSelection.Clone()
	return &out
}

// ValidateForExecution выбирает способ исполнения в том же CAS, что и одобрение.
// Повтор разрешён только для прежнего способа и неизменных параметров.
func (r *Request) ValidateForExecution(in TransitionInput, mode string, selection *ResearchSelection) ([]DomainEvent, error) {
	if mode == "" {
		mode = ExecutionModeLegacy
	}
	if mode != ExecutionModeLegacy && mode != ExecutionModeResearch {
		return nil, ErrExecutionModeConflict
	}
	if current := r.ExecutionMode(); current != "" && current != mode {
		return nil, ErrExecutionModeConflict
	}
	if mode == ExecutionModeResearch {
		if selection == nil || selection.Validate() != nil || selection.OfferRevision != r.offer.Revision() {
			return nil, ErrResearchSelectionConflict
		}
		if r.researchSelection != nil && !r.researchSelection.Equal(*selection) {
			return nil, ErrResearchSelectionConflict
		}
	} else if selection != nil {
		return nil, ErrResearchSelectionConflict
	}
	events, err := r.validateApproved(in)
	if err != nil {
		return nil, err
	}
	r.executionMode = mode
	if selection != nil && r.researchSelection == nil {
		value := selection.Clone()
		r.researchSelection = &value
	}
	return events, nil
}

func validateExecutionSnapshot(s RequestSnapshot) error {
	if s.ExecutionMode != "" && s.ExecutionMode != ExecutionModeLegacy && s.ExecutionMode != ExecutionModeResearch {
		return fmt.Errorf("%w: неизвестный способ исполнения", ErrInvalidSnapshot)
	}
	if s.ExecutionMode == ExecutionModeResearch {
		if s.ResearchSelection == nil || s.ResearchSelection.Validate() != nil {
			return fmt.Errorf("%w: отсутствует утверждённое поручение исследования", ErrInvalidSnapshot)
		}
	} else if s.ResearchSelection != nil {
		return fmt.Errorf("%w: параметры исследования не соответствуют способу исполнения", ErrInvalidSnapshot)
	}
	return nil
}
