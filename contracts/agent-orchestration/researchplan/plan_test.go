package researchplan

import (
	"fmt"
	"strings"
	"testing"
)

func validBrief(t *testing.T) SearchBrief {
	t.Helper()
	b, err := NewBrief(SearchBrief{ID: "b1", TenantID: "t1", Purpose: "lead-research", RequestID: "r1", IntentVersion: 1, ActorID: "human", Offer: "Внедрение CRM", ICP: "Генподрядчики коммерческого строительства", Goal: Goal{Quantity: 7, Unit: UnitCompany}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHumanGoalAndDefaultMarket(t *testing.T) {
	b := validBrief(t)
	if b.Goal.Quantity != 7 || b.Markets[0].Country != "RU" || b.Markets[0].Languages[0] != "ru" || b.Markets[0].Origin != OriginDefault {
		t.Fatalf("неверный снимок: %#v", b)
	}
	b.Goal.Quantity = 0
	if _, err := NewBrief(b); err == nil {
		t.Fatal("нулевая цель подменена default количеством")
	}
	b = validBrief(t)
	b.Markets = nil
	if b.Validate() == nil {
		t.Fatal("legacy чтение не должно автоматически получать RU")
	}
}

func TestInferredHardRestrictionsRejected(t *testing.T) {
	for _, group := range []string{"must", "exclude"} {
		t.Run(group, func(t *testing.T) {
			b := validBrief(t)
			c := Criterion{ID: "size", Text: "Только более 500 сотрудников", Origin: OriginInferred}
			if group == "must" {
				b.Criteria.Must = []Criterion{c}
			} else {
				b.Criteria.Exclude = []Criterion{c}
			}
			if b.Validate() == nil {
				t.Fatal("модель получила право изменить обязательные критерии")
			}
		})
	}
	b := validBrief(t)
	b.Criteria.Should = []Criterion{{ID: "size", Text: "Возможно крупные компании", Origin: OriginInferred}}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCountriesCapabilitiesAndExplicitUnknownLanguage(t *testing.T) {
	if len(Countries()) != 249 {
		t.Fatalf("страны: %d", len(Countries()))
	}
	b := validBrief(t)
	b.Markets = []MarketSelection{{Country: "UK"}}
	n, err := NewBrief(b)
	if err != nil || n.Markets[0].Country != "GB" || n.Markets[0].Languages[0] != "en" {
		t.Fatalf("alias: %v %#v", err, n.Markets)
	}
	for _, country := range []string{"ZZ", "EU", "GCC"} {
		b.Markets = []MarketSelection{{Country: country}}
		if _, err := NewBrief(b); err == nil {
			t.Fatalf("выдуманный или неразвёрнутый рынок: %s", country)
		}
	}
	b.Markets = []MarketSelection{{Country: "AG"}}
	if _, err := NewBrief(b); err == nil {
		t.Fatal("неизвестный профиль языка не должен выдумываться")
	}
	b.Markets = []MarketSelection{{Country: "AG", Languages: []string{"en"}}}
	if _, err := NewBrief(b); err != nil {
		t.Fatal(err)
	}
	s := SourceCapability{ID: "registry", Revision: "v1", SourceFamily: "registry", Countries: []string{"GB"}, Languages: []string{"en"}, Query: true, Available: true}
	if s.Supports("RU", "ru") || !s.Supports("GB", "en") {
		t.Fatal("профиль страны расширил capability")
	}
}

func TestBriefSnapshotCopiesProvenance(t *testing.T) {
	b := validBrief(t)
	b.Criteria.Must = []Criterion{{ID: "industry", Text: "Строительство", Origin: OriginHuman, EvidenceRefs: []string{"intent:1"}}}
	copy := b.Clone()
	b.Criteria.Must[0].EvidenceRefs[0] = "changed"
	b.Markets[0].Languages[0] = "en"
	if copy.Criteria.Must[0].EvidenceRefs[0] != "intent:1" || copy.Markets[0].Languages[0] != "ru" {
		t.Fatal("снимок разделяет память с изменяемым вводом")
	}
}

func TestBriefEvaluationBoundsBeforeAnyTool(t *testing.T) {
	b := validBrief(t)
	b.ICP = strings.Repeat("я", 8000)
	for i := 0; i < MaxEvaluatedCriteria; i++ {
		criterion := Criterion{ID: fmt.Sprintf("критерий-%d", i), Text: "Проверяемое условие", Origin: OriginHuman}
		switch {
		case i < 32:
			b.Criteria.Must = append(b.Criteria.Must, criterion)
		case i < 37:
			b.Criteria.Should = append(b.Criteria.Should, criterion)
		default:
			b.Criteria.Exclude = append(b.Criteria.Exclude, criterion)
		}
	}
	b.Criteria.Must[0].ID = strings.Repeat("я", 64)
	for i := 0; i < MaxCriteriaPerGroup; i++ {
		b.Criteria.Unknown = append(b.Criteria.Unknown, Criterion{ID: fmt.Sprintf("неизвестно-%d", i), Text: "Непроверенная гипотеза", Origin: OriginHuman})
	}
	if err := b.Validate(); err != nil {
		t.Fatalf("максимальный русский brief отклонён: %v", err)
	}
	for name, mutate := range map[string]func(*SearchBrief){
		"active-criteria": func(v *SearchBrief) {
			v.Criteria.Exclude = append(v.Criteria.Exclude, Criterion{ID: "лишний", Text: "Дополнительное ограничение", Origin: OriginHuman})
		},
		"id-bytes":    func(v *SearchBrief) { v.Criteria.Must[0].ID += "я" },
		"reserved-id": func(v *SearchBrief) { v.Criteria.Must[0].ID = "__human_icp" },
		"icp-bytes":   func(v *SearchBrief) { v.ICP = strings.Repeat("界", 5334) },
		"icp-utf8":    func(v *SearchBrief) { v.ICP = "профиль" + string([]byte{0xff}) },
		"scope-bytes": func(v *SearchBrief) { v.Purpose = strings.Repeat("я", 101) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := b.Clone()
			mutate(&invalid)
			if invalid.Validate() == nil {
				t.Fatal("задание вне технических границ допущено")
			}
			if _, err := NewBrief(invalid); err == nil {
				t.Fatal("конструктор обошёл проверку")
			}
		})
	}
}
