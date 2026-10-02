package publishing_test

import (
	"context"
	"errors"
	"testing"

	publishing "lead-gen/backend/contexts/publishing"
	platformadapter "lead-gen/backend/contexts/publishing/internal/adapters/platform"
	"lead-gen/backend/contexts/publishing/internal/app/command"
	"lead-gen/contracts/publishing/publicationcommand"
	"lead-gen/contracts/publishing/publicationresult"
)

func TestPublicationCommandContract(t *testing.T) {
	ctx := context.Background()
	slice := newSlice(t, platformadapter.NewDeterministic())
	port := publishing.NewPublicationCommand(slice)

	cmd := publicationcommand.Command{
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		DigestID:    "digest-1",
		CommunityID: "community-1",
		Period:      "2026-W10",
		Targets: []publicationcommand.Target{
			{
				PublicationID:  "pub-li",
				Platform:       "linkedin",
				Account:        "platform-account",
				IdempotencyKey: "key-li",
				Material:       publicationcommand.Material{Title: "Пост", Body: "Текст поста"},
			},
		},
	}
	result, err := port.Submit(ctx, cmd)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(result.Publications) != 1 || result.Publications[0].Status != "draft" {
		t.Fatalf("результат команды неверен: %+v", result)
	}

	// Публикуем и читаем исход через контракт publication-result.
	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-1"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reader := publishing.NewPublicationResult(slice)
	got, err := reader.Query(ctx, publicationresult.Query{TenantID: "tenant-1", PublicationID: "pub-li"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got.Records) != 1 {
		t.Fatalf("записей = %d, ожидалась 1", len(got.Records))
	}
	record := got.Records[0]
	if record.Status != publicationresult.StatusPublished || record.Address == "" || record.ApprovedBy != "operator-1" {
		t.Fatalf("запись неполна: %+v", record)
	}

	// Чужой тенант неотличим от отсутствующей записи.
	if _, err := reader.Query(ctx, publicationresult.Query{TenantID: "tenant-2", PublicationID: "pub-li"}); !errors.Is(err, publicationresult.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

func TestPublicationCommandRejectsInvalid(t *testing.T) {
	slice := newSlice(t, platformadapter.NewDeterministic())
	port := publishing.NewPublicationCommand(slice)

	if _, err := port.Submit(context.Background(), publicationcommand.Command{}); !errors.Is(err, publicationcommand.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestNilSliceContractsAreUnavailable(t *testing.T) {
	commandPort := publishing.NewPublicationCommand(nil)
	if _, err := commandPort.Submit(context.Background(), publicationcommand.Command{}); !errors.Is(err, publicationcommand.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	resultPort := publishing.NewPublicationResult(nil)
	if _, err := resultPort.Query(context.Background(), publicationresult.Query{TenantID: "t"}); !errors.Is(err, publicationresult.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
