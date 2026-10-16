package research

import (
	"fmt"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
	memory "lead-gen/contracts/lead-management/researchmemory"
	"strings"
	"testing"
)

func TestSourceFollowupsRequireActualScopedLinks(t *testing.T) {
	text := `[Контакты](/contacts?utm_campaign=a#team)
[About](/about)
[Контакты внешнего каталога](https://directory.example/contacts)
[Контакты](javascript:alert)
[Email](mailto:company@example.org)
[Раздел](#contacts)
[Цена](/prices)
Контакты должны быть по адресу /guessed, но это не ссылка.`
	receipt := ra.Receipt{Artifacts: []ra.Artifact{{URL: "https://company.example/", SHA256: "revision", Representation: "markdown", Content: text}}}
	got := SourceFollowups(receipt)
	if len(got) != 2 || got[0].URL != "https://company.example/contacts" || got[1].URL != "https://company.example/about" {
		t.Fatalf("неверные ссылки: %+v", got)
	}
	for _, link := range got {
		if !followupProven(receipt, link) {
			t.Fatal("реальный локатор не подтверждён")
		}
	}
	forged := got[0]
	forged.URL = "https://company.example/guessed"
	if followupProven(receipt, forged) {
		t.Fatal("выдуманная ссылка принята")
	}
	revoked := receipt
	revoked.Artifacts[0].SHA256 = "new-revision"
	if followupProven(revoked, got[0]) {
		t.Fatal("старый локатор принят для новой ревизии")
	}
}
func TestSourceFollowupsBoundedAndNoHostEscape(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&body, "[Контакты %d](/contacts/%d)\n", i, i)
	}
	body.WriteString("[Контакты](//attacker.example/contacts)")
	got := SourceFollowups(ra.Receipt{Artifacts: []ra.Artifact{{URL: "https://company.example/", SHA256: "revision", Representation: "markdown", Content: body.String()}}})
	if len(got) != 8 {
		t.Fatalf("нужен предел8 технических ссылок, получили%d", len(got))
	}
	for _, link := range got {
		if !strings.HasPrefix(link.URL, "https://company.example/") {
			t.Fatal(link.URL)
		}
	}
}

func TestGroundedDistinctUsesSameRegistryNamespace(t *testing.T) {
	a := []memory.IdentityGround{{Kind: "inn", Jurisdiction: "RU", Authority: "fns", Value: "7707083893"}}
	b := []memory.IdentityGround{{Kind: "inn", Jurisdiction: "RU", Authority: "fns", Value: "7736207543"}}
	if !groundedDistinct(a, b) {
		t.Fatal("два разных действующих ИНН не различены")
	}
	b[0].Jurisdiction = "KZ"
	if groundedDistinct(a, b) {
		t.Fatal("разные namespace ошибочно доказали различие")
	}
	b[0].Jurisdiction = "RU"
	b[0].Value = a[0].Value
	if groundedDistinct(a, b) {
		t.Fatal("общий сильный ID ошибочно доказал различие")
	}
}
