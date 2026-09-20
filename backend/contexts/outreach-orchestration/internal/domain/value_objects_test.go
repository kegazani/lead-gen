package domain

import (
	"errors"
	"testing"
	"time"
)

func mustWindow(t *testing.T) CampaignWindow {
	t.Helper()
	w, err := NewCampaignWindow(
		time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 18, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	return w
}

func mustChannel(t *testing.T, name string) ActionChannel {
	t.Helper()
	ch, err := ParseActionChannel(name)
	if err != nil {
		t.Fatalf("ParseActionChannel(%q): %v", name, err)
	}
	return ch
}

func testSpec(t *testing.T) DraftSpec {
	t.Helper()
	return DraftSpec{
		Material:  MaterialID("material-1"),
		Segment:   SegmentID("segment-1"),
		Recipient: RecipientID("recipient-1"),
		Channel:   mustChannel(t, ActionChannelEmailName),
	}
}

func TestNewCampaignWindowRejectsInvalidValue(t *testing.T) {
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 18, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		start time.Time
		end   time.Time
	}{
		"нулевое начало":    {time.Time{}, end},
		"нулевое окончание": {start, time.Time{}},
		"границы совпали":   {start, start},
		"начало позже":      {end, start},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCampaignWindow(tc.start, tc.end); !errors.Is(err, ErrInvalidCampaignWindow) {
				t.Fatalf("ожидалась ErrInvalidCampaignWindow, получено %v", err)
			}
		})
	}
}

func TestCampaignWindowIsImmutableValue(t *testing.T) {
	w := mustWindow(t)

	// Копия value object независима: она тот же интервал, но не тот же объект.
	copyOfWindow := w
	other := mustWindow(t)
	if !w.Equals(copyOfWindow) || !w.Equals(other) {
		t.Fatal("равные окна должны быть равны")
	}
	if w.IsZero() {
		t.Fatal("заданное окно не должно быть нулевым")
	}

	// Аксессоры возвращают значения, а не ссылки на внутреннее состояние.
	got := w.Start()
	got = got.Add(24 * time.Hour)
	if !w.Start().Equal(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("начало окна изменилось извне: %s", w.Start())
	}

	if !w.Contains(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("начало должно входить в окно")
	}
	if !w.Contains(time.Date(2026, 1, 1, 18, 0, 0, 0, time.UTC)) {
		t.Fatal("окончание должно входить в окно")
	}
	if w.Contains(time.Date(2026, 1, 1, 18, 0, 0, 1, time.UTC)) {
		t.Fatal("момент за окном не должен входить")
	}
	if (CampaignWindow{}).Contains(time.Now()) {
		t.Fatal("нулевое окно не должно ничего содержать")
	}
}

func TestParseActionChannel(t *testing.T) {
	for _, name := range []string{ActionChannelEmailName, ActionChannelCallName, ActionChannelFormName} {
		ch, err := ParseActionChannel(name)
		if err != nil {
			t.Fatalf("ParseActionChannel(%q): %v", name, err)
		}
		if ch.String() != name {
			t.Fatalf("канал %q нормализован в %q", name, ch.String())
		}
	}

	if ch, err := ParseActionChannel("  EMAIL "); err != nil || ch.String() != ActionChannelEmailName {
		t.Fatalf("регистр и пробелы должны игнорироваться: %v, %v", ch, err)
	}

	if _, err := ParseActionChannel("telegram"); !errors.Is(err, ErrUnknownActionChannel) {
		t.Fatalf("ожидалась ErrUnknownActionChannel, получено %v", err)
	}
	if !(ActionChannel{}).IsZero() {
		t.Fatal("нулевой канал должен быть не задан")
	}
}

func TestIdentifiersRejectEmptyValue(t *testing.T) {
	validators := map[string]func() error{
		"campaign_id": func() error { return CampaignID("").validate() },
		"draft_id":    func() error { return DraftID("  ").validate() },
		"decision_id": func() error { return DecisionID("").validate() },
		"tenant_id":   func() error { return TenantID("").validate() },
		"actor_id":    func() error { return ActorID("").validate() },
		"material_id": func() error { return MaterialID("").validate() },
		"segment_id":  func() error { return SegmentID("").validate() },
		"recipient":   func() error { return RecipientID("").validate() },
	}
	for name, validate := range validators {
		if err := validate(); !errors.Is(err, ErrEmptyID) {
			t.Fatalf("%s: ожидалась ErrEmptyID, получено %v", name, err)
		}
	}
}
