package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/landing"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// GetLandingPage — запрос сборки публикуемой страницы лендинга из уже
// воспроизведённого наполнения (landing-content). Запрос не меняет состояние:
// он собирает HTML/CSS/архив для размещения на хостинге клиента.
type GetLandingPage struct {
	AssetID  domain.ContentAssetID
	TenantID domain.TenantID

	// Description — метаописание страницы; пусто — метатег не выводится.
	Description string
	// Brand — бренд-комплект клиента; нулевой — нейтральное оформление.
	Brand domain.BrandKit
	// Company — реквизиты и контакты компании; нулевой — блок опускается.
	Company domain.CompanyProfile
	// Form — форма заявки; нулевая — блок заявки опускается.
	Form domain.LeadForm
}

// LandingPageAssembly — собранная страница лендинга для транспорта.
type LandingPageAssembly struct {
	Ref         domain.ArtifactRef
	Title       string
	Description string
	Language    string
	// HTML — самодостаточный документ с инлайн-стилями.
	HTML string
	// StyleSheet — CSS страницы.
	StyleSheet string
	// Archive — архив размещения (index.html + styles.css).
	Archive []byte
	// ExcludedContacts — исключённые личные контакты: факт исключения сообщается
	// оператору (требование 5.2).
	ExcludedContacts []string
}

// GetLandingPageHandler — use case сборки страницы лендинга.
type GetLandingPageHandler struct {
	packages ports.ContentPackageRepository
}

// NewGetLandingPageHandler собирает use case сборки страницы.
func NewGetLandingPageHandler(packages ports.ContentPackageRepository) *GetLandingPageHandler {
	return &GetLandingPageHandler{packages: packages}
}

// Handle загружает пакет, берёт наполнение лендинга и собирает страницу. Если
// пакета нет — ошибка «не найдено»; если лендинг ещё не воспроизведён —
// ErrArtifactNotReady.
func (h *GetLandingPageHandler) Handle(ctx context.Context, q GetLandingPage) (LandingPageAssembly, error) {
	pkg, err := h.packages.FindByID(ctx, q.AssetID)
	if err != nil {
		return LandingPageAssembly{}, err
	}
	if pkg.TenantID() != q.TenantID {
		return LandingPageAssembly{}, fmt.Errorf("%w: %s", ports.ErrContentPackageNotFound, q.AssetID)
	}
	content, ok := pkg.LandingContent()
	if !ok {
		return LandingPageAssembly{}, fmt.Errorf("%w: %s", ErrArtifactNotReady, q.AssetID)
	}

	page := landing.New(content, pkg.Locale().Language(), landing.Options{
		Description: q.Description,
		Brand:       q.Brand,
		Company:     q.Company,
		Form:        q.Form,
	})
	archive, err := page.Archive()
	if err != nil {
		return LandingPageAssembly{}, err
	}

	view := LandingPageAssembly{
		Ref:         domain.ArtifactRef(pkg.ID().String()),
		Title:       page.Title(),
		Description: page.Description(),
		Language:    page.Language(),
		HTML:        page.Document(),
		StyleSheet:  page.StyleSheet(),
		Archive:     archive,
	}
	for _, contact := range q.Company.ExcludedContacts() {
		view.ExcludedContacts = append(view.ExcludedContacts, fmt.Sprintf("%s: %s", contact.Kind(), contact.Value()))
	}
	return view, nil
}
