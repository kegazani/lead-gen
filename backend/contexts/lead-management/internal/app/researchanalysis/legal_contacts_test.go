package researchanalysis

import (
	"context"
	"strings"
	"testing"

	rules "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	model "lead-gen/contracts/integration-platform/llm"
)

// Фрагмент публичного документа https://profstal18.ru/, сохранённого реальным
// Firecrawl-прогоном 2026-10-15. Пустые строки отражают разметку источника.
func legalContactFixture() (string, model.ResearchSubject) {
	name := "ООО «ГК «Профсталь»"
	block := "**Наши реквизиты:**\n\n" + name + "\n\nИНН 9721179287КПП 772101001\n\nРоссия, УР, 426011, г. Ижевск, ул. К. Маркса, д.428А\n\nТелефоны: +7 (3412) 42-10-30, 72-53-10\n\nE-mail: tk@profstal18.ru"
	subject := model.ResearchSubject{Name: name, Kind: "legal-entity", Block: block, Country: "RU", CountryQuote: "Россия, УР, 426011, г. Ижевск, ул. К. Маркса, д.428А", Contacts: []model.ResearchContact{{Kind: "email", Value: "tk@profstal18.ru", Quote: strings.TrimPrefix(block, "**Наши реквизиты:**\n\n")}, {Kind: "phone", Value: "+7 (3412) 42-10-30", Quote: strings.TrimPrefix(block, "**Наши реквизиты:**\n\n")}}, Identifiers: []model.ResearchIdentifier{{Kind: "inn", Jurisdiction: "RU", Value: "9721179287", Quote: name + "\n\nИНН 9721179287КПП 772101001"}}, Criteria: []model.ResearchCriterionFinding{{ID: "__human_icp", Verdict: "unknown"}, {ID: "__human_market", Verdict: "match", Quote: "Россия, УР, 426011, г. Ижевск, ул. К. Маркса, д.428А"}}}
	return block, subject
}

func TestNamedLegalDetailsPreserveContactsWithoutClaimingICPOrRegistry(t *testing.T) {
	block, subject := legalContactFixture()
	req, _ := analysisFixture()
	req.Source.Content = "Преамбула 🏗️\n\n" + block + "\n\n[Контакты](/contacts/)"
	result, err := analyzerFor(t, []model.ResearchSubject{subject}).Analyze(context.Background(), req)
	if err != nil || len(result.Observations) != 1 {
		t.Fatalf("не сохранён субъект: %v %+v", err, result)
	}
	o := result.Observations[0]
	if o.Accepted || o.Reason != "criteria-evidence-missing" {
		t.Fatalf("контакты подменили неизвестный ICP: %+v", o)
	}
	contacts := map[string]string{}
	for _, claim := range o.Observation.Claims {
		if claim.Predicate != "email" && claim.Predicate != "phone" {
			continue
		}
		if claim.Method != "subject-block" || !strings.HasSuffix(claim.MethodVersion, "/legal-details/v1") || claim.Knowledge != "confirmed" || claim.Locator.Quote != block || !rules.ValidLocator(req.Source.Content, claim.Locator) {
			t.Fatalf("потеряно проверяемое основание: %+v", claim)
		}
		contacts[claim.Predicate] = claim.Value
	}
	if contacts["email"] != "tk@profstal18.ru" || contacts["phone"] != "+73412421030" {
		t.Fatalf("контакты потеряны: %+v, diagnostics=%v", contacts, result.Diagnostics)
	}
	if len(o.Observation.Identifiers) != 1 || o.Observation.Identifiers[0].Existence != "unknown" || o.Observation.Identifiers[0].Authority != "" {
		t.Fatal("сайт превратился в подтверждённый реестр")
	}
}

func TestLegalDetailsCannotAbsorbPublisherOrSecondCompany(t *testing.T) {
	cases := map[string]func(*model.ResearchSubject){
		"arbitrary-heading": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "Наши реквизиты", "Новости", 1)
		},
		"article-number": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "ИНН 9721179287КПП 772101001", "ID статьи: 9721179287", 1)
		},
		"article-to-publisher": func(s *model.ResearchSubject) {
			s.Block = "Статья об " + s.Name + ".\n\n" + strings.Replace(s.Block, "\n\n"+s.Name+"\n\n", "\n\nИздатель Каталог\n\n", 1)
		},
		"second-company": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "\n\nE-mail:", "\n\nООО Другая компания\n\nE-mail:", 1)
		},
		"second-inn": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "\n\nE-mail:", "\n\nИНН 7707083893\n\nE-mail:", 1)
		},
		"next-heading": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "\n\nE-mail:", "\n\n## Контакты редакции\n\nE-mail:", 1)
		},
		"narrative": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "\n\nE-mail:", "\n\nПо любым вопросам пишите издателю статьи.\n\nE-mail:", 1)
		},
		"not-standalone-name": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "\n\n"+s.Name+"\n\n", "\n\nНаш партнёр "+s.Name+"\n\n", 1)
		},
		"invalid-checksum": func(s *model.ResearchSubject) {
			s.Block = strings.ReplaceAll(s.Block, "9721179287", "9721179288")
			s.Identifiers[0].Value = "9721179288"
			s.Identifiers[0].Quote = strings.ReplaceAll(s.Identifiers[0].Quote, "9721179287", "9721179288")
		},
		"wrong-jurisdiction":  func(s *model.ResearchSubject) { s.Identifiers[0].Jurisdiction = "GB" },
		"missing-id-proposal": func(s *model.ResearchSubject) { s.Identifiers = []model.ResearchIdentifier{} },
		"source-link-mismatch": func(s *model.ResearchSubject) {
			s.Block = strings.Replace(s.Block, "E-mail: tk@profstal18.ru", "E-mail: [tk@profstal18.ru](mailto:publisher@other.example)", 1)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			_, subject := legalContactFixture()
			change(&subject)
			subject.Contacts = subject.Contacts[:1]
			subject.Contacts[0].Quote = subject.Block
			if legalDetailsContactEvidence(subject, subject.Contacts[0], []model.ResearchSubject{subject}) {
				t.Fatal("не доказанная связь принята как реквизиты")
			}
			req, _ := analysisFixture()
			req.Source.Content = subject.Block
			result, err := analyzerFor(t, []model.ResearchSubject{subject}).Analyze(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range result.Observations {
				for _, claim := range o.Observation.Claims {
					if claim.Predicate == "email" || claim.Predicate == "phone" {
						t.Fatal("отказ legal-details обойдён другим contact admission")
					}
				}
			}
		})
	}
}

func TestLegalDetailsRequiresIdentifierInsideContactEvidence(t *testing.T) {
	_, subject := legalContactFixture()
	contact := subject.Contacts[0]
	contact.Quote = subject.Name + "\n\nE-mail: " + contact.Value
	if legalDetailsContactEvidence(subject, contact, []model.ResearchSubject{subject}) {
		t.Fatal("контактная цитата без локального ID принята")
	}
	subject.Block += "\n\n" + strings.Repeat("адрес: Москва\n", 20)
	if legalDetailsContactEvidence(subject, subject.Contacts[0], []model.ResearchSubject{subject}) {
		t.Fatal("неограниченный раздел принят")
	}
}

func TestGBLegalDetailsRequireCompanyNumberLabel(t *testing.T) {
	s := model.ResearchSubject{Name: "Example Limited", Block: "## Company details\n\nExample Limited\n\nCompany number: 00000001\n\nRegistered office: London, United Kingdom\n\nEmail: sales@example.org", Identifiers: []model.ResearchIdentifier{{Kind: "company-number", Jurisdiction: "GB", Value: "00000001", Quote: "Company number: 00000001"}}}
	c := model.ResearchContact{Kind: "email", Value: "sales@example.org", Quote: s.Block}
	if !legalDetailsContactEvidence(s, c, []model.ResearchSubject{s}) {
		t.Fatal("валидный локальный номер GB не распознан")
	}
	s.Block = strings.Replace(s.Block, "Company number:", "Article number:", 1)
	c.Quote = s.Block
	if legalDetailsContactEvidence(s, c, []model.ResearchSubject{s}) {
		t.Fatal("ID статьи принят за company number")
	}
}
