package messagedelivery

import (
	"context"
	"testing"

	"lead-gen/contracts/content-production/contentversion"
)

// contentVersionStub — источник контракта content-version: отдаёт материал для
// ожидаемой ссылки, иначе — ненайденную версию.
func contentVersionStub(wantAsset, wantVersion, subject, body string) contentversion.Source {
	return contentversion.InProc(contentversion.InProcHandler{
		VersionFunc: func(_ context.Context, q contentversion.Query) (contentversion.Version, error) {
			if q.AssetID != wantAsset || q.VersionID != wantVersion {
				return contentversion.Version{}, contentversion.ErrNotFound
			}
			return contentversion.Version{Subject: subject, Body: body}, nil
		},
	})
}

// Фабрика собирает порт материала без именования internal-типов и передаёт тему и
// тело владельца как есть.
func TestNewContentMaterialResolverBuildsPort(t *testing.T) {
	materials := NewContentMaterialResolver(contentVersionStub("asset-1", "ver-1", "Тема", "Тело"))
	if materials == nil {
		t.Fatal("фабрика должна собрать резолвер")
	}
	material, err := materials.ResolveMaterial(context.Background(), "tenant-1", "asset-1@ver-1")
	if err != nil {
		t.Fatalf("ResolveMaterial: %v", err)
	}
	if material.Subject != "Тема" || material.Body != "Тело" {
		t.Fatalf("материал = %+v", material)
	}
}

// nil-источник — не собранный резолвер: тема и тело не выдумываются.
func TestNewContentMaterialResolverNilSourceNotConnected(t *testing.T) {
	if NewContentMaterialResolver(nil) != nil {
		t.Fatal("nil-источник не должен собирать резолвер")
	}
}

// Сквозная проверка моста: утверждённое действие со ссылкой на версию материала
// превращается в команду с темой и телом, разрешёнными у владельца контента.
func TestApprovedActionSourceResolvesMaterialFromContentVersion(t *testing.T) {
	materials := NewContentMaterialResolver(contentVersionStub("asset-1", "ver-1", "Тема письма", "Тело письма"))
	source, publisher, err := NewApprovedActionSource(
		deliveryTargetStub("lead@example.com", nil), materials, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionSource: %v", err)
	}

	action := sourceApprovedAction()
	action.MaterialVersionRef = "asset-1@ver-1"
	if err := publisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	command, ok, err := source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.Subject != "Тема письма" || command.Body != "Тело письма" {
		t.Fatalf("содержимое команды = %q / %q", command.Subject, command.Body)
	}
	if command.Address.String() != "lead@example.com" {
		t.Fatalf("адрес = %q", command.Address.String())
	}
}
