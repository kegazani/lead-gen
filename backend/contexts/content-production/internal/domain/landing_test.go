package domain

import (
	"errors"
	"testing"
	"time"
)

func TestBrandKit(t *testing.T) {
	t.Run("цвета канонизируются, пустой комплект допустим", func(t *testing.T) {
		kit, err := NewBrandKit("#AABBCC", " #112233 ", "Segoe UI, Arial", "ACME")
		if err != nil {
			t.Fatalf("NewBrandKit: %v", err)
		}
		if kit.PrimaryColor() != "#aabbcc" || kit.AccentColor() != "#112233" {
			t.Fatalf("цвета не канонизированы: %q, %q", kit.PrimaryColor(), kit.AccentColor())
		}
		if kit.IsZero() {
			t.Fatal("непустой комплект признан нулевым")
		}
	})

	t.Run("нулевой комплект — нейтральное оформление", func(t *testing.T) {
		kit, err := NewBrandKit("", "", "", "")
		if err != nil {
			t.Fatalf("NewBrandKit: %v", err)
		}
		if !kit.IsZero() {
			t.Fatal("пустой комплект не признан нулевым")
		}
	})

	t.Run("недопустимый цвет отвергается", func(t *testing.T) {
		if _, err := NewBrandKit("red", "", "", ""); !errors.Is(err, ErrInvalidBrandKit) {
			t.Fatalf("ожидалась ErrInvalidBrandKit, получено %v", err)
		}
	})

	t.Run("внедрение в CSS шрифтом отвергается", func(t *testing.T) {
		if _, err := NewBrandKit("", "", "Arial} body{display:none", ""); !errors.Is(err, ErrInvalidBrandKit) {
			t.Fatalf("ожидалась ErrInvalidBrandKit, получено %v", err)
		}
	})
}

func TestCompanyProfileExcludesPersonalContacts(t *testing.T) {
	general, err := NewCompanyContact(CompanyContactPhone, "+7 495 000-00-00", false)
	if err != nil {
		t.Fatalf("NewCompanyContact: %v", err)
	}
	personal, err := NewCompanyContact(CompanyContactEmail, "ivan.petrov@example.com", true)
	if err != nil {
		t.Fatalf("NewCompanyContact: %v", err)
	}
	profile, err := NewCompanyProfile("ООО Ромашка", "Москва, ул. Ленина, 1", []CompanyContact{general, personal})
	if err != nil {
		t.Fatalf("NewCompanyProfile: %v", err)
	}

	generalContacts := profile.GeneralContacts()
	if len(generalContacts) != 1 || generalContacts[0].Value() != "+7 495 000-00-00" {
		t.Fatalf("общие контакты неполны: %+v", generalContacts)
	}
	excluded := profile.ExcludedContacts()
	if len(excluded) != 1 || excluded[0].Value() != "ivan.petrov@example.com" {
		t.Fatalf("личный контакт не исключён: %+v", excluded)
	}
}

func TestCompanyProfileRequiresName(t *testing.T) {
	if _, err := NewCompanyProfile("   ", "", nil); !errors.Is(err, ErrCompanyNameRequired) {
		t.Fatalf("ожидалась ErrCompanyNameRequired, получено %v", err)
	}
}

func TestCompanyContactValidation(t *testing.T) {
	if _, err := NewCompanyContact("fax", "123", false); !errors.Is(err, ErrInvalidContactKind) {
		t.Fatalf("ожидалась ErrInvalidContactKind, получено %v", err)
	}
	if _, err := NewCompanyContact(CompanyContactEmail, "  ", false); !errors.Is(err, ErrInvalidContactValue) {
		t.Fatalf("ожидалась ErrInvalidContactValue, получено %v", err)
	}
}

func TestLandingMaterialsRequireDescription(t *testing.T) {
	if _, err := NewLandingMaterials("https://site.example", "  ", "", nil); !errors.Is(err, ErrMaterialsInsufficient) {
		t.Fatalf("ожидалась ErrMaterialsInsufficient, получено %v", err)
	}
	t.Run("описание без сайта достаточно", func(t *testing.T) {
		materials, err := NewLandingMaterials("", "Описываем бизнес", "", nil)
		if err != nil {
			t.Fatalf("NewLandingMaterials: %v", err)
		}
		if materials.HasSite() {
			t.Fatal("сайт не задан, но признан заданным")
		}
	})
}

func TestBuildLandingStructureFollowsMaterials(t *testing.T) {
	withoutProduct, err := NewLandingMaterials("", "Описываем бизнес", "", nil)
	if err != nil {
		t.Fatalf("NewLandingMaterials: %v", err)
	}
	structure, err := BuildLandingStructure(withoutProduct)
	if err != nil {
		t.Fatalf("BuildLandingStructure: %v", err)
	}
	roles := structure.Roles()
	if containsRole(roles, LandingBlockSolution) || containsRole(roles, LandingBlockProof) {
		t.Fatalf("блоки без подтверждения созданы: %v", roles)
	}
	if !containsRole(roles, LandingBlockProblem) || !containsRole(roles, LandingBlockAction) {
		t.Fatalf("обязательные блоки отсутствуют: %v", roles)
	}

	withProduct, err := NewLandingMaterials("https://site.example", "Описываем бизнес", "Продукт", []string{"Коммерческое предложение"})
	if err != nil {
		t.Fatalf("NewLandingMaterials: %v", err)
	}
	full, err := BuildLandingStructure(withProduct)
	if err != nil {
		t.Fatalf("BuildLandingStructure: %v", err)
	}
	if !containsRole(full.Roles(), LandingBlockSolution) || !containsRole(full.Roles(), LandingBlockProof) {
		t.Fatalf("подтверждённые блоки не созданы: %v", full.Roles())
	}

	gaps := withoutProduct.Gaps()
	if !containsRole(gaps, LandingBlockSolution) || !containsRole(gaps, LandingBlockProof) {
		t.Fatalf("нехватка материалов не сообщена: %v", gaps)
	}
}

func TestLandingStructureApproval(t *testing.T) {
	materials, err := NewLandingMaterials("", "Описываем бизнес", "Продукт", nil)
	if err != nil {
		t.Fatalf("NewLandingMaterials: %v", err)
	}
	structure, err := BuildLandingStructure(materials)
	if err != nil {
		t.Fatalf("BuildLandingStructure: %v", err)
	}
	if err := structure.EnsureApproved(); !errors.Is(err, ErrStructureNotApproved) {
		t.Fatalf("генерация не заблокирована до утверждения: %v", err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := structure.Approve("operator-1", at); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := structure.EnsureApproved(); err != nil {
		t.Fatalf("утверждение не учтено: %v", err)
	}
	if !structure.Approved() || structure.ApprovedBy() != "operator-1" || !structure.ApprovedAt().Equal(at) {
		t.Fatalf("утверждение неполно: %+v", structure)
	}
}

func containsRole(roles []LandingBlockRole, want LandingBlockRole) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func TestLeadFormMinimalComposition(t *testing.T) {
	t.Run("минимальная форма собирается", func(t *testing.T) {
		form, err := NewLeadForm("https://client.example/leads", []LeadFormField{LeadFormFieldEmail, LeadFormFieldName})
		if err != nil {
			t.Fatalf("NewLeadForm: %v", err)
		}
		fields := form.Fields()
		if len(fields) != 2 || fields[0] != LeadFormFieldName || fields[1] != LeadFormFieldEmail {
			t.Fatalf("поля не упорядочены: %+v", fields)
		}
	})

	t.Run("приёмник обязателен", func(t *testing.T) {
		if _, err := NewLeadForm("  ", []LeadFormField{LeadFormFieldName, LeadFormFieldEmail}); !errors.Is(err, ErrFormActionRequired) {
			t.Fatalf("ожидалась ErrFormActionRequired, получено %v", err)
		}
	})

	t.Run("нужны имя и контакт", func(t *testing.T) {
		if _, err := NewLeadForm("https://x", []LeadFormField{LeadFormFieldName}); !errors.Is(err, ErrInvalidLeadForm) {
			t.Fatalf("ожидалась ErrInvalidLeadForm, получено %v", err)
		}
	})

	t.Run("неизвестное и повторяющееся поле отвергаются", func(t *testing.T) {
		if _, err := NewLeadForm("https://x", []LeadFormField{LeadFormFieldName, "company"}); !errors.Is(err, ErrInvalidLeadForm) {
			t.Fatalf("ожидалась ErrInvalidLeadForm, получено %v", err)
		}
		if _, err := NewLeadForm("https://x", []LeadFormField{LeadFormFieldName, LeadFormFieldEmail, LeadFormFieldEmail}); !errors.Is(err, ErrInvalidLeadForm) {
			t.Fatalf("ожидалась ErrInvalidLeadForm, получено %v", err)
		}
	})

	t.Run("опасная схема приёмника отвергается", func(t *testing.T) {
		if _, err := NewLeadForm("javascript:alert(1)", []LeadFormField{LeadFormFieldName, LeadFormFieldEmail}); !errors.Is(err, ErrInvalidLeadForm) {
			t.Fatalf("ожидалась ErrInvalidLeadForm, получено %v", err)
		}
	})
}
