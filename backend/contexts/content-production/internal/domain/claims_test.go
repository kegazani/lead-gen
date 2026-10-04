package domain

import (
	"strings"
	"testing"
)

func TestUnsourcedClaims(t *testing.T) {
	materials, err := NewLandingMaterials("", "Мы разрабатываем сайты для бизнеса", "", nil)
	if err != nil {
		t.Fatalf("NewLandingMaterials: %v", err)
	}

	t.Run("текст в границах материалов не имеет несогласованных сведений", func(t *testing.T) {
		claims := UnsourcedClaims("Мы разрабатываем сайты для бизнеса", materials)
		if len(claims) != 0 {
			t.Fatalf("ожидался пустой список, получено %v", claims)
		}
	})

	t.Run("числа, сроки и гарантии вне материалов отвергаются", func(t *testing.T) {
		claims := UnsourcedClaims("Гарантируем рост продаж на 30% за 5 дней", materials)
		for _, want := range []string{"30", "5", "гарант"} {
			if !containsString(claims, want) {
				t.Fatalf("несогласованное сведение %q не найдено: %v", want, claims)
			}
		}
	})

	t.Run("сведения из материалов не считаются несогласованными", func(t *testing.T) {
		withFacts, err := NewLandingMaterials("", "Гарантируем 30% за 5 дней", "", nil)
		if err != nil {
			t.Fatalf("NewLandingMaterials: %v", err)
		}
		claims := UnsourcedClaims("Гарантируем 30% за 5 дней", withFacts)
		if len(claims) != 0 {
			t.Fatalf("сведения из материалов отвергнуты: %v", claims)
		}
	})
}

func TestDetectDiscrepancies(t *testing.T) {
	discrepancies := DetectDiscrepancies("Экономия до 30%", []string{"Экономия до 50%"})
	if len(discrepancies) != 2 {
		t.Fatalf("ожидалось два расхождения, получено %v", discrepancies)
	}
	for _, item := range discrepancies {
		if !strings.Contains(item, "30%") && !strings.Contains(item, "50%") {
			t.Fatalf("расхождение не называет показатель: %q", item)
		}
	}

	if got := DetectDiscrepancies("Экономия до 30%", []string{"Экономия до 30%"}); len(got) != 0 {
		t.Fatalf("совпадающие источники дали расхождение: %v", got)
	}
	if got := DetectDiscrepancies("", nil); got != nil {
		t.Fatalf("пустые источники дали расхождение: %v", got)
	}
}

func TestVerbatimMatches(t *testing.T) {
	source := "Мы поставляем промышленное оборудование по всей России"
	text := "Наша компания активно растёт. Мы поставляем промышленное оборудование по всей России."

	t.Run("дословный фрагмент обнаружен", func(t *testing.T) {
		matches := VerbatimMatches(text, []string{source}, nil)
		if len(matches) != 1 {
			t.Fatalf("ожидалось одно совпадение, получено %v", matches)
		}
		if !strings.Contains(matches[0], "промышленное оборудование") {
			t.Fatalf("совпадение неполно: %q", matches[0])
		}
	})

	t.Run("совпадение в реквизитах не считается заимствованием", func(t *testing.T) {
		requisites := "ООО Ромашка ИНН 7701234567 город Москва"
		matches := VerbatimMatches(requisites, []string{requisites}, []string{requisites})
		if len(matches) != 0 {
			t.Fatalf("реквизиты приняты за заимствование: %v", matches)
		}
	})

	t.Run("короткий оборот не считается заимствованием", func(t *testing.T) {
		matches := VerbatimMatches("Надёжное решение", []string{"Надёжное решение"}, nil)
		if len(matches) != 0 {
			t.Fatalf("короткий оборот принят за заимствование: %v", matches)
		}
	})
}

func TestExcludePersonalData(t *testing.T) {
	text := strings.Join([]string{
		"Мы производим мебель на заказ",
		"Директор: Иван Петров",
		"sales@client.example",
		"ivan.petrov@example.com",
		"Телефон: +7 495 000-00-00",
		"Мобильный: +7 999 000-11-22",
	}, "\n")

	clean, excluded := ExcludePersonalData(text)
	for _, wantGone := range []string{"Иван Петров", "ivan.petrov@example.com", "+7 999 000-11-22"} {
		if strings.Contains(clean, wantGone) {
			t.Fatalf("персональное сведение %q попало в очищенный текст: %s", wantGone, clean)
		}
	}
	for _, wantKept := range []string{"мебель на заказ", "sales@client.example", "+7 495 000-00-00"} {
		if !strings.Contains(clean, wantKept) {
			t.Fatalf("общее сведение %q потеряно: %s", wantKept, clean)
		}
	}
	if len(excluded) < 3 {
		t.Fatalf("факт исключения сообщён неполно: %v", excluded)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
