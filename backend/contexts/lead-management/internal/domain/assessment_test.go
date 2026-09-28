package domain

import (
	"errors"
	"testing"
	"time"
)

// fitsEvidence — корректное свидетельство прямого заявления для вердикта
// «Подходит».
func fitsEvidence() Evidence {
	return Evidence{
		Criterion:  "нужен инструмент выстраивания отдела продаж",
		Fact:       "компания публикует вакансию Head of Sales",
		SourceURL:  "https://example.com/careers/head-of-sales",
		Reasoning:  "наём руководителя отдела продаж указывает на построение процесса продаж",
		SignalKind: SignalDirect,
	}
}

func TestCreateLeadAssessmentVerdicts(t *testing.T) {
	tests := []struct {
		name        string
		verdict     AssessmentVerdict
		signal      SignalKind
		evidence    []Evidence
		wantVerdict AssessmentVerdict
		wantSignal  SignalKind
	}{
		{
			name:        "прямое заявление",
			verdict:     VerdictFits,
			signal:      SignalDirect,
			evidence:    []Evidence{fitsEvidence()},
			wantVerdict: VerdictFits,
			wantSignal:  SignalDirect,
		},
		{
			name:    "косвенный сигнал",
			verdict: VerdictFits,
			signal:  SignalIndirect,
			evidence: []Evidence{{
				Criterion:  "нужен инструмент выстраивания отдела продаж",
				Fact:       "компания наняла трёх менеджеров по продажам за квартал",
				SourceURL:  "https://example.com/blog/team-growth",
				Reasoning:  "быстрый рост команды продаж указывает на растущий процесс",
				SignalKind: SignalIndirect,
			}},
			wantVerdict: VerdictFits,
			wantSignal:  SignalIndirect,
		},
		{
			name:    "совпадение по профилю",
			verdict: VerdictHypothesis,
			signal:  SignalProfile,
			evidence: []Evidence{{
				Criterion:  "B2B SaaS, 50-200 сотрудников",
				Fact:       "компания относится к отрасли B2B SaaS и размеру 120 человек",
				SourceURL:  "https://example.com/about",
				Reasoning:  "профиль соответствует целевой аудитории, но сигнала потребности нет",
				SignalKind: SignalProfile,
				Missing:    "свидетельство интереса к инструменту выстраивания отдела продаж",
			}},
			wantVerdict: VerdictHypothesis,
			wantSignal:  SignalProfile,
		},
		{
			name:    "установлено несоответствие",
			verdict: VerdictUnsuitable,
			signal:  SignalProfile,
			evidence: []Evidence{{
				Criterion:  "B2B SaaS, 50-200 сотрудников",
				Fact:       "компания работает только с физическими лицами",
				SourceURL:  "https://example.com/pricing",
				Reasoning:  "модель B2C исключает заданный тип клиента",
				SignalKind: SignalProfile,
				Mismatch:   "компания не соответствует обязательному типу клиента B2B",
			}},
			wantVerdict: VerdictUnsuitable,
			wantSignal:  SignalProfile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := CreateLeadAssessment(CreateLeadAssessmentParams{
				JobID:      "job-1",
				CompanyID:  "example.com",
				Verdict:    tc.verdict,
				SignalKind: tc.signal,
				Evidence:   tc.evidence,
				CreatedAt:  baseTime,
			})
			if err != nil {
				t.Fatalf("CreateLeadAssessment: %v", err)
			}
			if a.Verdict() != tc.wantVerdict {
				t.Fatalf("Verdict %q, ожидался %q", a.Verdict(), tc.wantVerdict)
			}
			if a.SignalKind() != tc.wantSignal {
				t.Fatalf("SignalKind %q, ожидался %q", a.SignalKind(), tc.wantSignal)
			}
			if a.JobID() != "job-1" || a.CompanyID() != "example.com" {
				t.Fatalf("ключ (%q, %q), ожидался (job-1, example.com)", a.JobID(), a.CompanyID())
			}
			if len(a.Evidence()) != len(tc.evidence) {
				t.Fatalf("свидетельств %d, ожидалось %d", len(a.Evidence()), len(tc.evidence))
			}
		})
	}
}

func TestCreateLeadAssessmentRejectsInvariantViolations(t *testing.T) {
	valid := []Evidence{fitsEvidence()}
	hypothesisOnly := []Evidence{{Criterion: "c", Fact: "f", SourceURL: "https://x", Reasoning: "r", SignalKind: SignalProfile}}
	unsuitableOnly := []Evidence{{Criterion: "c", Fact: "f", SourceURL: "https://x", Reasoning: "r", SignalKind: SignalProfile}}

	tests := []struct {
		name   string
		params CreateLeadAssessmentParams
		want   error
	}{
		{
			name:   "пустой job id",
			params: CreateLeadAssessmentParams{CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: valid},
			want:   ErrEmptyAssessmentJobID,
		},
		{
			name:   "job id из пробелов",
			params: CreateLeadAssessmentParams{JobID: "   ", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: valid},
			want:   ErrEmptyAssessmentJobID,
		},
		{
			name:   "пустой company id",
			params: CreateLeadAssessmentParams{JobID: "job-1", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: valid},
			want:   ErrEmptyAssessmentCompanyID,
		},
		{
			name:   "company id из пробелов",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: " \t ", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: valid},
			want:   ErrEmptyAssessmentCompanyID,
		},
		{
			name:   "неизвестный вердикт",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: "maybe", SignalKind: SignalDirect, Evidence: valid},
			want:   ErrInvalidVerdict,
		},
		{
			name:   "неизвестный тип сигнала",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: "maybe", Evidence: valid},
			want:   ErrInvalidSignalKind,
		},
		{
			name:   "оценка без свидетельств",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect},
			want:   ErrAssessmentEvidenceRequired,
		},
		{
			name:   "«Подходит» с сигналом по профилю",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalProfile, Evidence: valid},
			want:   ErrInvalidSignalKind,
		},
		{
			name: "«Подходит» без критерия",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect,
				Evidence: []Evidence{{Fact: "f", SourceURL: "https://x", Reasoning: "r"}}},
			want: ErrAssessmentEvidenceRequired,
		},
		{
			name: "«Подходит» без факта",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect,
				Evidence: []Evidence{{Criterion: "c", SourceURL: "https://x", Reasoning: "r"}}},
			want: ErrAssessmentEvidenceRequired,
		},
		{
			name: "«Подходит» без источника",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect,
				Evidence: []Evidence{{Criterion: "c", Fact: "f", Reasoning: "r"}}},
			want: ErrAssessmentEvidenceRequired,
		},
		{
			name: "«Подходит» без обоснования",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect,
				Evidence: []Evidence{{Criterion: "c", Fact: "f", SourceURL: "https://x"}}},
			want: ErrAssessmentEvidenceRequired,
		},
		{
			name:   "гипотеза не по профилю",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictHypothesis, SignalKind: SignalDirect, Evidence: hypothesisOnly},
			want:   ErrInvalidSignalKind,
		},
		{
			name:   "гипотеза без недостающего свидетельства",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictHypothesis, SignalKind: SignalProfile, Evidence: hypothesisOnly},
			want:   ErrAssessmentEvidenceRequired,
		},
		{
			name:   "«Не подходит» без несоответствия",
			params: CreateLeadAssessmentParams{JobID: "job-1", CompanyID: "example.com", Verdict: VerdictUnsuitable, SignalKind: SignalProfile, Evidence: unsuitableOnly},
			want:   ErrAssessmentEvidenceRequired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateLeadAssessment(tc.params)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ожидалась %v, получено %v", tc.want, err)
			}
		})
	}
}

func TestLeadAssessmentEvidenceSeparatesFactFromInterpretation(t *testing.T) {
	a, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalIndirect,
		Evidence: []Evidence{{
			Criterion:  "нужен инструмент выстраивания отдела продаж",
			Fact:       "компания наняла трёх менеджеров по продажам",
			SourceURL:  "https://example.com/blog",
			Reasoning:  "рост команды продаж связан с потребностью в процессе",
			SignalKind: SignalIndirect,
		}},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}
	evidence := a.Evidence()
	if len(evidence) != 1 {
		t.Fatalf("свидетельств %d, ожидалось 1", len(evidence))
	}
	if evidence[0].Fact == evidence[0].Reasoning {
		t.Fatal("факт и интерпретация не разделены")
	}
	if evidence[0].Fact != "компания наняла трёх менеджеров по продажам" {
		t.Fatalf("факт искажён: %q", evidence[0].Fact)
	}
	if evidence[0].Reasoning != "рост команды продаж связан с потребностью в процессе" {
		t.Fatalf("обоснование искажено: %q", evidence[0].Reasoning)
	}

	// Возвращённый срез — копия: правка снаружи не меняет агрегат.
	evidence[0].Fact = "подменённый факт"
	if a.Evidence()[0].Fact != "компания наняла трёх менеджеров по продажам" {
		t.Fatal("Evidence() вернул общий срез вместо копии")
	}
}

func TestLeadAssessmentsIndependentPerJob(t *testing.T) {
	fits, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []Evidence{fitsEvidence()},
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment job-1: %v", err)
	}
	unsuitable, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-2",
		CompanyID:  "example.com",
		Verdict:    VerdictUnsuitable,
		SignalKind: SignalProfile,
		Evidence: []Evidence{{
			Criterion:  "другой запрос",
			Fact:       "компания B2C",
			SourceURL:  "https://example.com/pricing",
			Reasoning:  "не соответствует типу клиента",
			SignalKind: SignalProfile,
			Mismatch:   "B2C вместо B2B",
		}},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment job-2: %v", err)
	}
	if fits.CompanyID() != unsuitable.CompanyID() {
		t.Fatalf("ожидалась одна компания, получено %q и %q", fits.CompanyID(), unsuitable.CompanyID())
	}

	// Изменение оценки в одном задании не влияет на другое.
	err = unsuitable.ChangeVerdict(VerdictFits, SignalDirect, []Evidence{fitsEvidence()}, baseTime.Add(time.Hour))
	if err != nil {
		t.Fatalf("ChangeVerdict job-2: %v", err)
	}
	if fits.Verdict() != VerdictFits {
		t.Fatalf("оценка job-1 изменилась при правке job-2: %q", fits.Verdict())
	}
	if unsuitable.Verdict() != VerdictFits {
		t.Fatalf("оценка job-2 не изменилась: %q", unsuitable.Verdict())
	}
	if fits.UpdatedAt() != baseTime {
		t.Fatalf("UpdatedAt job-1 изменился: %v", fits.UpdatedAt())
	}
}

func TestLeadAssessmentEvents(t *testing.T) {
	a, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []Evidence{fitsEvidence()},
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}
	events := a.Events()
	if len(events) != 1 {
		t.Fatalf("событий %d, ожидалось 1", len(events))
	}
	assessed, ok := events[0].(LeadAssessed)
	if !ok {
		t.Fatalf("первое событие %T, ожидалось LeadAssessed", events[0])
	}
	if assessed.EventName() != "lead-assessed" {
		t.Fatalf("EventName %q, ожидалось lead-assessed", assessed.EventName())
	}
	if assessed.JobID != "job-1" || assessed.CompanyID != "example.com" || assessed.Verdict != "fits" {
		t.Fatalf("поля LeadAssessed некорректны: %+v", assessed)
	}
	if again := a.Events(); len(again) != 0 {
		t.Fatalf("повторный Events() вернул %d событий, ожидалось 0", len(again))
	}

	if err := a.ChangeVerdict(VerdictUnsuitable, SignalProfile, []Evidence{{
		Criterion: "c", Fact: "f", SourceURL: "https://x", Reasoning: "r", SignalKind: SignalProfile, Mismatch: "m",
	}}, baseTime.Add(time.Hour)); err != nil {
		t.Fatalf("ChangeVerdict: %v", err)
	}
	revisedEvents := a.Events()
	if len(revisedEvents) != 1 {
		t.Fatalf("событий после ревизии %d, ожидалось 1", len(revisedEvents))
	}
	revised, ok := revisedEvents[0].(LeadAssessmentRevised)
	if !ok {
		t.Fatalf("событие ревизии %T, ожидалось LeadAssessmentRevised", revisedEvents[0])
	}
	if revised.EventName() != "lead-assessment-revised" {
		t.Fatalf("EventName %q, ожидалось lead-assessment-revised", revised.EventName())
	}
	if revised.Verdict != "unsuitable" {
		t.Fatalf("вердикт ревизии %q, ожидался unsuitable", revised.Verdict)
	}
}

func TestChangeVerdictRejectsInvalidState(t *testing.T) {
	a, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []Evidence{fitsEvidence()},
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}

	if err := a.ChangeVerdict(VerdictHypothesis, SignalProfile, []Evidence{fitsEvidence()}, baseTime); !errors.Is(err, ErrAssessmentEvidenceRequired) {
		t.Fatalf("ожидалась ErrAssessmentEvidenceRequired, получено %v", err)
	}
	// Неудачная ревизия не меняет состояние агрегата и не эмитит событие.
	if a.Verdict() != VerdictFits || a.UpdatedAt() != baseTime {
		t.Fatal("неудачная ревизия изменила состояние агрегата")
	}
	if len(a.Events()) != 1 {
		t.Fatal("неудачная ревизия эмитила лишнее событие")
	}
}

func TestRehydrateLeadAssessmentRoundTrip(t *testing.T) {
	a, err := CreateLeadAssessment(CreateLeadAssessmentParams{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []Evidence{fitsEvidence()},
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}
	a.Events() // буфер событий не является частью снимка

	restored, err := RehydrateLeadAssessment(a.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateLeadAssessment: %v", err)
	}
	if restored.JobID() != a.JobID() || restored.CompanyID() != a.CompanyID() ||
		restored.Verdict() != a.Verdict() || restored.SignalKind() != a.SignalKind() ||
		restored.CreatedAt() != a.CreatedAt() || restored.UpdatedAt() != a.UpdatedAt() {
		t.Fatal("восстановленная оценка не совпадает с исходной")
	}
	if len(restored.Evidence()) != 1 || restored.Evidence()[0] != fitsEvidence() {
		t.Fatalf("свидетельства не восстановлены: %+v", restored.Evidence())
	}
	if len(restored.Events()) != 0 {
		t.Fatalf("восстановление эмитило %d событий, ожидалось 0", len(restored.Events()))
	}
}

func TestRehydrateLeadAssessmentRejectsBadSnapshot(t *testing.T) {
	valid := LeadAssessmentSnapshot{
		JobID:      "job-1",
		CompanyID:  "example.com",
		Verdict:    VerdictFits,
		SignalKind: SignalDirect,
		Evidence:   []Evidence{fitsEvidence()},
		CreatedAt:  baseTime,
		UpdatedAt:  baseTime,
	}

	if _, err := RehydrateLeadAssessment(LeadAssessmentSnapshot{CompanyID: "example.com", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: []Evidence{fitsEvidence()}}); !errors.Is(err, ErrEmptyAssessmentJobID) {
		t.Fatalf("ожидалась ErrEmptyAssessmentJobID, получено %v", err)
	}
	if _, err := RehydrateLeadAssessment(LeadAssessmentSnapshot{JobID: "job-1", Verdict: VerdictFits, SignalKind: SignalDirect, Evidence: []Evidence{fitsEvidence()}}); !errors.Is(err, ErrEmptyAssessmentCompanyID) {
		t.Fatalf("ожидалась ErrEmptyAssessmentCompanyID, получено %v", err)
	}

	badVerdict := valid
	badVerdict.Verdict = "maybe"
	if _, err := RehydrateLeadAssessment(badVerdict); !errors.Is(err, ErrInvalidSnapshot) || !errors.Is(err, ErrInvalidVerdict) {
		t.Fatalf("ожидалась ErrInvalidSnapshot+ErrInvalidVerdict, получено %v", err)
	}

	badSignal := valid
	badSignal.SignalKind = "maybe"
	if _, err := RehydrateLeadAssessment(badSignal); !errors.Is(err, ErrInvalidSnapshot) || !errors.Is(err, ErrInvalidSignalKind) {
		t.Fatalf("ожидалась ErrInvalidSnapshot+ErrInvalidSignalKind, получено %v", err)
	}

	noEvidence := valid
	noEvidence.Evidence = nil
	if _, err := RehydrateLeadAssessment(noEvidence); !errors.Is(err, ErrInvalidSnapshot) || !errors.Is(err, ErrAssessmentEvidenceRequired) {
		t.Fatalf("ожидалась ErrInvalidSnapshot+ErrAssessmentEvidenceRequired, получено %v", err)
	}
}
