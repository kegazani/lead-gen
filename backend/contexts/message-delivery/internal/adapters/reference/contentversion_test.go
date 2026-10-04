package reference_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/reference"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/content-production/contentversion"
)

// fakeSource — подмена контракта content-version: запоминает последний запрос и
// отдаёт заданную версию или ошибку.
type fakeSource struct {
	version contentversion.Version
	err     error
	query   contentversion.Query
	calls   int
}

func (f *fakeSource) Version(_ context.Context, q contentversion.Query) (contentversion.Version, error) {
	f.calls++
	f.query = q
	return f.version, f.err
}

// Тема и тело владельца возвращаются как есть — адаптер не подменяет содержимое.
func TestContentMaterialResolverReturnsSubjectAndBody(t *testing.T) {
	owner := &fakeSource{version: contentversion.Version{Subject: "Тема письма", Body: "Тело письма"}}
	adapter, err := reference.NewContentMaterialResolver(owner)
	if err != nil {
		t.Fatalf("NewContentMaterialResolver: %v", err)
	}
	material, err := adapter.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-2")
	if err != nil {
		t.Fatalf("ResolveMaterial: %v", err)
	}
	if material.Subject != "Тема письма" || material.Body != "Тело письма" {
		t.Fatalf("материал = %+v", material)
	}
	if owner.calls != 1 {
		t.Fatalf("владелец вызван %d раз, ожидался 1", owner.calls)
	}
	// Ссылка разобрана в пакет и версию, тенант проброшен владельцу.
	if owner.query.AssetID != "asset-1" || owner.query.VersionID != "ver-2" || owner.query.TenantID != "tenant-1" {
		t.Fatalf("запрос = %+v", owner.query)
	}
}

// Ссылка вида asset_id@version_id разбирается на пакет и версию.
func TestContentMaterialResolverSplitsAssetVersionRef(t *testing.T) {
	cases := []struct {
		name      string
		ref       string
		wantAsset string
		wantVer   string
	}{
		{name: "пакет и версия", ref: "asset-1@ver-2", wantAsset: "asset-1", wantVer: "ver-2"},
		{name: "без разделителя", ref: "ver-only", wantAsset: "", wantVer: "ver-only"},
		{name: "пустой пакет", ref: "@ver-2", wantAsset: "", wantVer: "ver-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := &fakeSource{version: contentversion.Version{Body: "тело"}}
			adapter, err := reference.NewContentMaterialResolver(owner)
			if err != nil {
				t.Fatalf("NewContentMaterialResolver: %v", err)
			}
			if _, err := adapter.ResolveMaterial(context.Background(), "tenant-1", tc.ref); err != nil {
				t.Fatalf("ResolveMaterial: %v", err)
			}
			if owner.query.AssetID != tc.wantAsset || owner.query.VersionID != tc.wantVer {
				t.Fatalf("asset=%q ver=%q, ожидалось asset=%q ver=%q",
					owner.query.AssetID, owner.query.VersionID, tc.wantAsset, tc.wantVer)
			}
		})
	}
}

// Ненайденная версия — неразрешимая ссылка порта, а не недоступность.
func TestContentMaterialResolverMapsNotFound(t *testing.T) {
	owner := &fakeSource{err: contentversion.ErrNotFound}
	adapter, err := reference.NewContentMaterialResolver(owner)
	if err != nil {
		t.Fatalf("NewContentMaterialResolver: %v", err)
	}
	_, err = adapter.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-2")
	if !errors.Is(err, ports.ErrReferenceUnresolved) {
		t.Fatalf("ошибка = %v, ожидалась ErrReferenceUnresolved", err)
	}
	if errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatal("ненайденная версия не должна выдаваться за недоступность")
	}
}

// Недоступность владельца переводится в недоступность резолвера.
func TestContentMaterialResolverMapsUnavailable(t *testing.T) {
	owner := &fakeSource{err: contentversion.ErrUnavailable}
	adapter, err := reference.NewContentMaterialResolver(owner)
	if err != nil {
		t.Fatalf("NewContentMaterialResolver: %v", err)
	}
	_, err = adapter.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-2")
	if !errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrResolverUnavailable", err)
	}
}

// Отмена и дедлайн пробрасываются как есть, чтобы вызывающий различал их.
func TestContentMaterialResolverPassesContextErrors(t *testing.T) {
	owner := &fakeSource{err: context.DeadlineExceeded}
	adapter, err := reference.NewContentMaterialResolver(owner)
	if err != nil {
		t.Fatalf("NewContentMaterialResolver: %v", err)
	}
	_, err = adapter.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-2")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка = %v, ожидался context.DeadlineExceeded", err)
	}
}

// Реальный in-proc адаптер контракта: успешное чтение отдаёт тему и тело.
func TestContentMaterialResolverWithInProcResolves(t *testing.T) {
	owner := contentversion.InProc(contentversion.InProcHandler{
		VersionFunc: func(_ context.Context, q contentversion.Query) (contentversion.Version, error) {
			if q.AssetID != "asset-1" || q.VersionID != "ver-1" || q.TenantID != "tenant-1" {
				return contentversion.Version{}, contentversion.ErrNotFound
			}
			return contentversion.Version{Subject: "Тема", Body: "Тело"}, nil
		},
	})
	adapter, err := reference.NewContentMaterialResolver(owner)
	if err != nil {
		t.Fatalf("NewContentMaterialResolver: %v", err)
	}
	material, err := adapter.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-1")
	if err != nil {
		t.Fatalf("ResolveMaterial: %v", err)
	}
	if material.Subject != "Тема" || material.Body != "Тело" {
		t.Fatalf("материал = %+v", material)
	}
}

// Незаданный владелец — ошибка конфигурации, а не молчаливый адаптер.
func TestNewContentMaterialResolverRejectsNilOwner(t *testing.T) {
	if _, err := reference.NewContentMaterialResolver(nil); err == nil {
		t.Fatal("сборка без реализации владельца должна отвергаться")
	}
}
