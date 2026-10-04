package reference

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/content-production/contentversion"
)

// ContentMaterialResolver реализует ports.MaterialResolver поверх контракта
// content-version владельца content-production. Состояния не хранит, безопасен
// для конкурентного использования.
type ContentMaterialResolver struct {
	impl contentversion.Source
}

// ContentMaterialResolver реализует порт разрешения материала письма.
var _ ports.MaterialResolver = (*ContentMaterialResolver)(nil)

// NewContentMaterialResolver связывает источник версий владельца с портом
// контекста. Отсутствие реализации — ошибка конфигурации, а не молчаливая
// недоступность на пути доставки.
func NewContentMaterialResolver(impl contentversion.Source) (*ContentMaterialResolver, error) {
	if impl == nil {
		return nil, errors.New("reference: content-version: не задан источник владельца")
	}
	return &ContentMaterialResolver{impl: impl}, nil
}

// ResolveMaterial реализует порт: разбирает ссылку вида
// `asset_id@version_id`, читает версию у владельца и переводит её в материал
// письма (тема, тело). Исход контракта переводится в исход порта (см.
// mapContentVersionError); тема и тело берутся у владельца и не выдумываются.
func (r *ContentMaterialResolver) ResolveMaterial(ctx context.Context, tenantID, materialVersionRef string) (ports.Material, error) {
	if r == nil || r.impl == nil {
		return ports.Material{}, fmt.Errorf("%w: адаптер content-version не собран", ports.ErrResolverUnavailable)
	}
	assetID, versionID := parseMaterialVersionRef(materialVersionRef)
	version, err := r.impl.Version(ctx, contentversion.Query{
		AssetID:   assetID,
		VersionID: versionID,
		TenantID:  tenantID,
	})
	if err != nil {
		return ports.Material{}, mapContentVersionError(err)
	}
	return ports.Material{Subject: version.Subject, Body: version.Body}, nil
}

// parseMaterialVersionRef разбирает ссылку материала. Разделитель — '@':
// `asset_id@version_id`. Без разделителя AssetID пуст, а VersionID — вся строка,
// поэтому владелец сам трактует ссылку как версию без явного пакета.
func parseMaterialVersionRef(ref string) (assetID, versionID string) {
	assetID, versionID, found := strings.Cut(ref, "@")
	if !found {
		return "", ref
	}
	return assetID, versionID
}

// mapContentVersionError переводит ошибку контракта content-version в исход порта.
// Отмена и дедлайн пробрасываются как есть (их различает вызывающий); ненайденная
// версия — неразрешимая ссылка (ports.ErrReferenceUnresolved); недоступность
// владельца и всё прочее — недоступность резолвера (ports.ErrResolverUnavailable).
// Отказ не выдаётся за разрешённый материал.
func mapContentVersionError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, contentversion.ErrNotFound):
		return fmt.Errorf("%w: %v", ports.ErrReferenceUnresolved, err)
	case errors.Is(err, contentversion.ErrUnavailable):
		return fmt.Errorf("%w: %v", ports.ErrResolverUnavailable, err)
	default:
		return fmt.Errorf("%w: %v", ports.ErrResolverUnavailable, err)
	}
}
