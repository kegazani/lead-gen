package publicationcommand_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/publishing/publicationcommand"
)

func validCommand() publicationcommand.Command {
	return publicationcommand.Command{
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		DigestID:    "digest-1",
		CommunityID: "community-1",
		Period:      "2026-W10",
		Targets: []publicationcommand.Target{
			{
				PublicationID:  "pub-1",
				Platform:       publicationcommand.PlatformLinkedInName,
				Account:        "acct",
				IdempotencyKey: "key-1",
				Material:       publicationcommand.Material{Body: "текст"},
			},
		},
	}
}

func TestValidateAcceptsValidCommand(t *testing.T) {
	if err := validCommand().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsInvalidCommand(t *testing.T) {
	cases := map[string]func(*publicationcommand.Command){
		"без тенанта":          func(c *publicationcommand.Command) { c.TenantID = "" },
		"без площадок":         func(c *publicationcommand.Command) { c.Targets = nil },
		"неизвестная площадка": func(c *publicationcommand.Command) { c.Targets[0].Platform = "instagram" },
		"пустой текст":         func(c *publicationcommand.Command) { c.Targets[0].Material.Body = "  " },
		"без ключа":            func(c *publicationcommand.Command) { c.Targets[0].IdempotencyKey = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := validCommand()
			mutate(&cmd)
			if err := cmd.Validate(); err == nil {
				t.Fatal("ожидалась ошибка валидации")
			}
		})
	}
}

func TestInProcUnsetIsUnavailable(t *testing.T) {
	port := publicationcommand.InProc(publicationcommand.InProcHandler{})
	if _, err := port.Submit(context.Background(), validCommand()); !errors.Is(err, publicationcommand.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
