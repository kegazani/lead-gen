package components

import (
	"strings"
	"testing"
)

func TestTruncateTextUsesUnicodeCodePointsAndAddsEllipsisOnlyWhenNeeded(t *testing.T) {
	short := strings.Repeat("я", 100)
	if got := TruncateText(short, 100); got != short {
		t.Fatalf("текст ровно в лимит изменён: %q", got)
	}
	long := short + "東京"
	if got, want := TruncateText(long, 100), short+"…"; got != want {
		t.Fatalf("сокращение = %q, ожидалось %q", got, want)
	}
	if got := TruncateText("a🙂b", 2); got != "a🙂…" {
		t.Fatalf("Unicode code points обрезаны неверно: %q", got)
	}
}
