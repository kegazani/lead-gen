package transport

import (
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

// testInstant — фиксированный момент для детерминированных контрактов.
var testInstant = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// testEnvelope собирает валидный конверт контракта с заданным именем и версией.
func testEnvelope(name, version string) contracts.Envelope {
	return contracts.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          name,
		SchemaVersion: version,
	}
}

// validApprovedAction собирает проверяемый контракт approved-action.
func validApprovedAction() contracts.ApprovedAction {
	a := contracts.NewApprovedAction(testEnvelope(
		contracts.ContractApprovedAction, contracts.ApprovedActionSchemaVersion))
	a.ApprovedActionID = "act-1"
	a.CampaignID = "camp-1"
	a.DraftID = "draft-1"
	a.DraftVersion = 3
	a.DecisionID = "dec-1"
	a.Channel = "email"
	a.RecipientRef = "recipient-ref-1"
	a.DecidedAt = testInstant.Add(time.Minute)
	return a
}

// validEngagementEvent собирает проверяемый контракт engagement-event.
func validEngagementEvent() contracts.EngagementEvent {
	return contracts.EngagementEvent{
		Envelope: testEnvelope(
			contracts.ContractEngagementEvent, contracts.EngagementEventSchemaVersion),
		EventType:    contracts.EngagementEventOpened,
		CampaignID:   "camp-1",
		DraftID:      "draft-1",
		RecipientRef: "recipient-ref-1",
	}
}

// validProcessingAuthority собирает проверяемый контракт processing-authority.
func validProcessingAuthority() contracts.ProcessingAuthority {
	return contracts.ProcessingAuthority{
		Envelope: testEnvelope(
			contracts.ContractProcessingAuthority, contracts.ProcessingAuthoritySchemaVersion),
		PolicyID:     "policy-1",
		Scope:        "tenant:tenant-1",
		AllowedZones: []string{"green"},
		LegalBasis:   "consent",
		EffectiveAt:  testInstant,
	}
}
