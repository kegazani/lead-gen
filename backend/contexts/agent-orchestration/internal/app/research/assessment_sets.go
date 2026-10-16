package research

import (
	"encoding/json"
	"reflect"

	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

func memberAssessments(member rm.Member) []Assessment {
	if len(member.AssessmentSets) > 0 {
		return append([]Assessment(nil), member.AssessmentSets...)
	}
	// Совместимое чтение уже сохранённой полной оценки; её собственные точные
	// refs всё равно перепроверяются по памяти перед повторным зачётом.
	if member.ReceiptID != "" && len(member.EvidenceRefs) > 0 {
		return []Assessment{{ObservationID: member.ReceiptID, CriteriaVersion: member.CriteriaVersion, Accepted: member.CriteriaAccepted, EmailReady: member.EmailReady, Reason: member.Reason, EvidenceRefs: append([]string(nil), member.EvidenceRefs...)}}
	}
	return nil
}
func appendAssessment(base []Assessment, next Assessment) ([]Assessment, error) {
	if next.ObservationID == "" {
		return base, nil
	}
	for _, existing := range base {
		if existing.ObservationID == next.ObservationID {
			if !reflect.DeepEqual(existing, next) {
				return nil, rm.ErrConflict
			}
			return base, nil
		}
	}
	return append(append([]Assessment(nil), base...), next), nil
}
func sameAssessmentSets(left, right []Assessment) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}
