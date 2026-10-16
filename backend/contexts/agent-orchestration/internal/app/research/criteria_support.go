package research

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	rm "lead-gen/contracts/agent-orchestration/researchmission"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

// CriteriaSupport — текущая проверка полных оценок этой миссии. Не содержит
// channel/email readiness: она требует отдельной проверки текущего адресата.
type CriteriaSupport struct {
	Accepted     bool
	Reason       string
	EvidenceRefs []string
}

// resolveCriteriaSupport вызывается вне mission-транзакции. Снимок dossier
// проверяется повторно при refresh; CAS вызывающего слоя защищает версию миссии.
// Поиск по одному predicate/criteriaVersion запрещён: допустимы только точные
// ссылки из ранее сохранённых assessment sets этой миссии.
func resolveCriteriaSupport(ctx context.Context, store memory.Port, scope rm.Scope, entityID string, criteriaVersion int, assessments []Assessment) (CriteriaSupport, error) {
	return CheckCriteriaSupport(ctx, store, scope, entityID, criteriaVersion, assessments)
}

// CheckCriteriaSupport публикуется только через composition API контекста.
func CheckCriteriaSupport(ctx context.Context, store memory.Port, scope rm.Scope, entityID string, criteriaVersion int, assessments []Assessment) (CriteriaSupport, error) {
	out := CriteriaSupport{Reason: "criteria-evidence-missing"}
	if store == nil || scope.TenantID == "" || scope.PurposeID == "" || entityID == "" || criteriaVersion < 1 {
		return out, rm.ErrInvalid
	}
	dossier, err := store.Get(ctx, memory.GetRequest{Scope: memory.Scope{TenantID: scope.TenantID, Purpose: scope.PurposeID}, EntityID: entityID})
	if errors.Is(err, memory.ErrRevoked) || errors.Is(err, memory.ErrNotFound) {
		out.Reason = "criteria-evidence-unavailable"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if dossier.Entity.ID != entityID {
		return out, rm.ErrConflict
	}
	sources := map[string]memory.Source{}
	for _, source := range dossier.Sources {
		if source.ID == "" {
			continue
		}
		if _, exists := sources[source.ID]; exists {
			return out, rm.ErrConflict
		}
		sources[source.ID] = source
	}
	now := time.Now()
	var positive []string
	for _, assessment := range assessments {
		if assessment.CriteriaVersion != criteriaVersion || assessment.ObservationID == "" || len(assessment.EvidenceRefs) == 0 {
			continue
		}
		negative := !assessment.Accepted && (assessment.Reason == "criteria-not-satisfied" || assessment.Reason == "market-evidence-mismatch")
		if !assessment.Accepted && !negative {
			continue
		}
		if !assessmentSupported(assessment, criteriaVersion, dossier.Assertions, sources, now) {
			continue
		}
		// Явное действующее противоречие не теряется из-за порядка прихода
		// наблюдений; требуется пересмотр вместо OR старого accepted=true.
		if negative {
			return CriteriaSupport{Reason: "criteria-review-required", EvidenceRefs: append([]string(nil), assessment.EvidenceRefs...)}, nil
		}
		if positive == nil {
			positive = append([]string(nil), assessment.EvidenceRefs...)
		}
	}
	if positive != nil {
		return CriteriaSupport{Accepted: true, Reason: "criteria-evidence-supported", EvidenceRefs: positive}, nil
	}
	return out, nil
}

type criteriaEvidenceRef struct {
	observationID        string
	version              int
	criterionID, verdict string
	start, end           int
}

func parseCriteriaEvidenceRef(raw string) (criteriaEvidenceRef, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) < 6 {
		return criteriaEvidenceRef{}, false
	}
	version, e1 := strconv.Atoi(parts[1])
	start, e2 := strconv.Atoi(parts[len(parts)-2])
	end, e3 := strconv.Atoi(parts[len(parts)-1])
	ref := criteriaEvidenceRef{observationID: parts[0], version: version, criterionID: strings.Join(parts[2:len(parts)-3], ":"), verdict: parts[len(parts)-3], start: start, end: end}
	if e1 != nil || e2 != nil || e3 != nil || ref.observationID == "" || ref.criterionID == "" || version < 1 || start < 0 || end <= start || (ref.verdict != "match" && ref.verdict != "mismatch") {
		return criteriaEvidenceRef{}, false
	}
	return ref, true
}

func assessmentSupported(assessment Assessment, version int, assertions []memory.Assertion, sources map[string]memory.Source, now time.Time) bool {
	seen := map[string]bool{}
	for _, raw := range assessment.EvidenceRefs {
		if seen[raw] {
			return false
		}
		seen[raw] = true
		ref, ok := parseCriteriaEvidenceRef(raw)
		if !ok || ref.observationID != assessment.ObservationID || ref.version != version {
			return false
		}
		predicate := fmt.Sprintf("criteria-evidence:%d:%s:%s", version, ref.criterionID, ref.verdict)
		found := false
		for _, assertion := range assertions {
			if assertion.ObservationID != ref.observationID || assertion.Predicate != predicate || assertion.Locator.Start != ref.start || assertion.Locator.End != ref.end {
				continue
			}
			if assertion.Access != "available" || assertion.SupportState != "satisfied" || (assertion.Knowledge != "observed" && assertion.Knowledge != "confirmed") || assertion.Freshness == "stale" || (assertion.ValidFrom != nil && assertion.ValidFrom.After(now)) || (assertion.ValidUntil != nil && !assertion.ValidUntil.After(now)) {
				continue
			}
			source, present := sources[assertion.SourceID]
			if !present || source.Revision == "" || ref.end > len(source.Content) || assertion.Locator.Quote == "" || source.Content[ref.start:ref.end] != assertion.Locator.Quote {
				continue
			}
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return len(seen) > 0
}
