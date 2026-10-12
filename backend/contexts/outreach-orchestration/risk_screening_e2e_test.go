package outreachorchestration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	composition "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// Владельцы фактов риска для интеграционного сценария risk-screening: детермини-
// рованные двойники опубликованных контрактов. Двойники — только тест: на
// продуктовом пути порты собирает точка входа (assembleRiskFacts).

// riskRecipients — контракт delivery-target: ссылка получателя → адрес.
type riskRecipients struct {
	addresses   map[string]string
	unavailable bool
}

func (r riskRecipients) ResolveRecipient(_ context.Context, _, ref string) (deliverytarget.Recipient, error) {
	if r.unavailable {
		return deliverytarget.Recipient{}, deliverytarget.ErrUnavailable
	}
	address, ok := r.addresses[ref]
	if !ok {
		return deliverytarget.Recipient{}, deliverytarget.ErrRejected
	}
	return deliverytarget.Recipient{Address: address}, nil
}

// riskSuppression — контракт suppression-policy: адрес → подавлен.
type riskSuppression struct {
	suppressed  map[string]bool
	unavailable bool
}

func (s riskSuppression) Check(_ context.Context, q suppressionpolicy.SuppressionQuery) (suppressionpolicy.SuppressionDecision, error) {
	if s.unavailable {
		return suppressionpolicy.SuppressionDecision{}, suppressionpolicy.ErrUnavailable
	}
	return suppressionpolicy.SuppressionDecision{Suppressed: s.suppressed[q.Contact]}, nil
}

// riskVerification — контракт address-verification: ссылка → подтверждён.
type riskVerification struct {
	verified    map[string]bool
	unavailable bool
}

func (v riskVerification) Verification(_ context.Context, q addressverification.Query) (addressverification.Snapshot, error) {
	if v.unavailable {
		return addressverification.Snapshot{}, addressverification.ErrUnavailable
	}
	return addressverification.Snapshot{
		RecipientRef: q.RecipientRef,
		Status:       addressverification.StatusDeliverable,
		Verified:     v.verified[q.RecipientRef],
	}, nil
}

// TestRiskScreeningExcludesSuppressedAndUnverifiedDrafts — сквозной сценарий 6.1:
// черновик с активной записью стоп-листа и черновик с неподтверждённым адресом
// исключаются из пакета с причинами; чистые черновики входят в пакет.
func TestRiskScreeningExcludesSuppressedAndUnverifiedDrafts(t *testing.T) {
	const (
		campaignID = "campaign-risk"
		material   = "material-risk"
		segment    = "segment-risk"
		cleanCount = 50 // порог пакета продукта: не меньше 50 черновиков
	)
	risk := composition.NewRiskFacts(composition.RiskFactsPorts{
		Recipients: riskRecipients{addresses: map[string]string{
			"recipient-clean": "clean@example.com",
			"recipient-stop":  "stop@example.com",
			"recipient-unver": "unver@example.com",
		}},
		Suppression: riskSuppression{suppressed: map[string]bool{"stop@example.com": true}},
		Verification: riskVerification{verified: map[string]bool{
			"recipient-clean": true,
			"recipient-stop":  true,
			"recipient-unver": false,
		}},
	})
	fixture := newE2EFixtureWithRisk(t, risk)
	createCampaignThroughHTTP(t, fixture, campaignID)

	for i := 0; i < cleanCount; i++ {
		createDraftThroughHTTP(t, fixture, campaignID,
			fmt.Sprintf("draft-clean-%03d", i), material, segment, "recipient-clean")
	}
	createDraftThroughHTTP(t, fixture, campaignID, "draft-stop", material, segment, "recipient-stop")
	createDraftThroughHTTP(t, fixture, campaignID, "draft-unver", material, segment, "recipient-unver")

	view := requestBatchComposition(t, fixture, campaignID)
	if len(view.Batches) != 1 || view.Batches[0].Size != cleanCount {
		t.Fatalf("пакет чистых черновиков: %+v", view.Batches)
	}
	excluded := make(map[string][]string, len(view.Excluded))
	for _, item := range view.Excluded {
		excluded[item.DraftID] = item.Reasons
	}
	if got := excluded["draft-stop"]; len(got) != 1 || got[0] != "stop-rule-triggered" {
		t.Fatalf("черновик подавленного контакта: причины %v", got)
	}
	if got := excluded["draft-unver"]; len(got) != 1 || got[0] != "address-unverified" {
		t.Fatalf("черновик неподтверждённого адреса: причины %v", got)
	}
}

// TestRiskScreeningUnavailableOwnerIsHonestRefusal — сценарий 6.1: недоступный
// владелец не даёт «рисков нет» — состав честно недоступен.
func TestRiskScreeningUnavailableOwnerIsHonestRefusal(t *testing.T) {
	const campaignID = "campaign-risk-unavailable"
	risk := composition.NewRiskFacts(composition.RiskFactsPorts{
		Recipients:   riskRecipients{unavailable: true},
		Suppression:  riskSuppression{},
		Verification: riskVerification{},
	})
	fixture := newE2EFixtureWithRisk(t, risk)
	createCampaignThroughHTTP(t, fixture, campaignID)
	createDraftThroughHTTP(t, fixture, campaignID, "draft-1", "material-1", "segment-1", "recipient-1")

	url := fmt.Sprintf("%s/campaigns/%s/batches/composition?tenant_id=%s&actor_id=%s",
		fixture.server.URL, campaignID, e2eTenant, e2eOperator)
	response, _ := doJSON(t, http.MethodGet, url, nil)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("недоступный владелец: статус %d, ожидался 503", response.StatusCode)
	}
}
