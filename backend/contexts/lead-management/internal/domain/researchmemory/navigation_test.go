package researchmemory

import (
	contract "lead-gen/contracts/lead-management/researchmemory"
	"testing"
)

func TestIdentityNavigationRequiresActualSameOriginLink(t *testing.T) {
	source := contract.Source{URL: "https://company.example.org/", Representation: "markdown", Content: "Alpha Company\n[Наши контакты](/contacts)\n"}
	loc, ok := NavigationCandidate(source, "https://company.example.org/contacts")
	if !ok || !ValidLocator(source.Content, loc) || loc.Quote != "[Наши контакты](/contacts)" {
		t.Fatalf("локатор %v %+v", ok, loc)
	}
	for _, target := range []string{"https://other.example.org/contacts", "http://company.example.org/contacts", "https://company.example.org/legal", "https://company.example.org/contacts?branch=2"} {
		if _, ok = NavigationCandidate(source, target); ok {
			t.Fatalf("переход выдуман: %s", target)
		}
	}
	source.Content = "Alpha Company /contacts"
	if _, ok = NavigationCandidate(source, "https://company.example.org/contacts"); ok {
		t.Fatal("текст пути выдан за ссылку")
	}
	source.Content = `<a href="/contacts">Contacts</a>`
	source.Representation = "html"
	if _, ok = NavigationCandidate(source, "https://company.example.org/contacts"); !ok {
		t.Fatal("сохранённый HTML href не найден")
	}
}
