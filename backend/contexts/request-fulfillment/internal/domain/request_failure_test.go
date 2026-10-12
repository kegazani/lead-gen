package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// inputFail собирает переход с машинным кодом и текстом причины.
func inputFail(n int, code, reason string) domain.TransitionInput {
	in := inputReason(n, reason)
	in.ReasonCode = code
	return in
}

// TestFailedStatusKnownAndTerminal — статус failed объявлен доменом и терминален.
func TestFailedStatusKnownAndTerminal(t *testing.T) {
	found := false
	for _, status := range domain.RequestStatuses() {
		if status == domain.StatusFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("RequestStatuses() не содержит failed: %v", domain.RequestStatuses())
	}
	if !domain.StatusFailed.IsTerminal() {
		t.Fatal("failed должен быть терминальным")
	}
}

// TestFailFromProcessing — processing → failed с событием request-execution-failed,
// несущим код и текст причины.
func TestFailFromProcessing(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}

	events, err := request.Fail(inputFail(4, "search_failed", "поиск упал"))
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestExecutionFailed {
		t.Fatalf("события = %v, ожидалось request-execution-failed", names(events))
	}
	if request.Status() != domain.StatusFailed {
		t.Fatalf("статус = %q, ожидался failed", request.Status())
	}

	failed, ok := events[0].(domain.RequestExecutionFailed)
	if !ok {
		t.Fatalf("событие = %T, ожидался RequestExecutionFailed", events[0])
	}
	if failed.ReasonCode != "search_failed" || failed.Reason != "поиск упал" {
		t.Fatalf("причина = %q/%q, ожидалась search_failed/поиск упал", failed.ReasonCode, failed.Reason)
	}
	if failed.From != domain.StatusProcessing || failed.To != domain.StatusFailed {
		t.Fatalf("переход = %q → %q", failed.From, failed.To)
	}
}

// TestFailRequiresReason — сбой без причины отвергается.
func TestFailRequiresReason(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(input(4)); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
}

// TestFailRequiresReasonCode — сбой с текстом, но без машинного кода причины,
// отвергается: код обязателен так же, как текст.
func TestFailRequiresReasonCode(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputReason(4, "сбой без кода")); !errors.Is(err, domain.ErrInvalidReasonCode) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidReasonCode", err)
	}
}

// TestFailRequiresReasonWithValidCode — текст причины обязателен независимо от
// машинного кода: валидный код при пустом тексте не применяет переход.
func TestFailRequiresReasonWithValidCode(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputFail(4, "search_failed", "")); !errors.Is(err, domain.ErrReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался processing (переход не применён)", request.Status())
	}
}

// TestFailRejectsUnknownReasonCode — машинный код вне согласованного набора
// отвергается доменной ошибкой, а заявка остаётся в processing.
func TestFailRejectsUnknownReasonCode(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputFail(4, "unknown_code", "сбой")); !errors.Is(err, domain.ErrInvalidReasonCode) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidReasonCode", err)
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался processing (переход не применён)", request.Status())
	}
}

// TestFailAcceptsAllReasonCodes — все согласованные коды причин допустимы.
func TestFailAcceptsAllReasonCodes(t *testing.T) {
	for _, code := range []string{"search_failed", "draft_failed", "handoff_failed", "transition_rejected", "workflow_timed_out"} {
		t.Run(code, func(t *testing.T) {
			request := newRequest(t)
			toValidated(t, request)
			if _, err := request.StartProcessing(input(3)); err != nil {
				t.Fatalf("StartProcessing: %v", err)
			}
			if _, err := request.Fail(inputFail(4, code, "сбой")); err != nil {
				t.Fatalf("Fail(%q): %v", code, err)
			}
			if request.Status() != domain.StatusFailed {
				t.Fatalf("статус = %q, ожидался failed", request.Status())
			}
		})
	}
}

// TestFailForbiddenOutsideProcessing — сбой запрещён из нетерминального
// не-processing статуса и из терминального статуса.
func TestFailForbiddenOutsideProcessing(t *testing.T) {
	t.Run("из submitted", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.Fail(inputFail(1, "search_failed", "x")); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
		}
	})
	t.Run("из validated", func(t *testing.T) {
		request := newRequest(t)
		toValidated(t, request)
		if _, err := request.Fail(inputFail(3, "search_failed", "x")); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
		}
	})
	t.Run("из терминального fulfilled", func(t *testing.T) {
		request := newRequest(t)
		toValidated(t, request)
		if _, err := request.StartProcessing(input(3)); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		if _, err := request.Fulfill(input(4)); err != nil {
			t.Fatalf("Fulfill: %v", err)
		}
		if _, err := request.Fail(inputFail(5, "search_failed", "x")); !errors.Is(err, domain.ErrTerminalState) {
			t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
		}
	})
}

// TestFailIdempotent — повторный Fail по уже failed заявке — no-op: статус, версия
// и число событий не меняются.
func TestFailIdempotent(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputFail(4, "search_failed", "поиск упал")); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	version := request.Version()
	transitions := len(request.Transitions())
	request.PullDomainEvents()

	events, err := request.Fail(inputFail(5, "search_failed", "поиск упал"))
	if err != nil {
		t.Fatalf("повтор Fail: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор эмитировал события: %v", names(events))
	}
	if request.Status() != domain.StatusFailed || request.Version() != version || len(request.Transitions()) != transitions {
		t.Fatalf("повтор изменил состояние: статус %q, версия %d, переходов %d",
			request.Status(), request.Version(), len(request.Transitions()))
	}
}

// TestRerunFailedToValidated — явный повторный запуск failed → validated, после
// чего стандартный путь validated → processing снова запускает исполнение.
func TestRerunFailedToValidated(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputFail(4, "handoff_failed", "передача упала")); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// failed терминален для автоматических переходов: StartProcessing из него
	// отвергается.
	if _, err := request.StartProcessing(input(5)); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("StartProcessing из failed: ошибка = %v, ожидалась ErrTerminalState", err)
	}

	if _, err := request.Rerun(input(6)); err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	if request.Status() != domain.StatusValidated {
		t.Fatalf("статус = %q, ожидался validated", request.Status())
	}

	events, err := request.StartProcessing(input(7))
	if err != nil {
		t.Fatalf("StartProcessing после re-run: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestProcessingStarted {
		t.Fatalf("события = %v, ожидалось request-processing-started", names(events))
	}
	if request.Status() != domain.StatusProcessing {
		t.Fatalf("статус = %q, ожидался processing", request.Status())
	}
}

// TestRerunOnlyFromFailed — повторный запуск допустим только из failed.
func TestRerunOnlyFromFailed(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Rerun(input(4)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("из processing: ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
}

// TestRerunRejectedFromOtherStatuses — повторный запуск допустим только из failed:
// из другого терминального статуса даёт ErrTerminalState, из прочих активных —
// ErrInvalidTransition, а повтор по validated — no-op.
func TestRerunRejectedFromOtherStatuses(t *testing.T) {
	t.Run("из submitted — ErrInvalidTransition", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.Rerun(input(1)); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
		}
	})
	t.Run("из validated — no-op", func(t *testing.T) {
		request := newRequest(t)
		toValidated(t, request)
		if _, err := request.Rerun(input(2)); err != nil {
			t.Fatalf("Rerun: %v", err)
		}
		if request.Status() != domain.StatusValidated {
			t.Fatalf("статус = %q, ожидался validated", request.Status())
		}
	})
	t.Run("из cancelled — ErrTerminalState", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.Cancel(inputReason(1, "клиент отказался")); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if _, err := request.Rerun(input(2)); !errors.Is(err, domain.ErrTerminalState) {
			t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
		}
	})
	t.Run("из rejected — ErrTerminalState", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.StartValidation(input(1)); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := request.Reject(inputReason(2, "не подходит")); err != nil {
			t.Fatalf("Reject: %v", err)
		}
		if _, err := request.Rerun(input(3)); !errors.Is(err, domain.ErrTerminalState) {
			t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
		}
	})
	t.Run("из fulfilled — ErrTerminalState", func(t *testing.T) {
		request := newRequest(t)
		toValidated(t, request)
		if _, err := request.StartProcessing(input(3)); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		if _, err := request.Fulfill(input(4)); err != nil {
			t.Fatalf("Fulfill: %v", err)
		}
		if _, err := request.Rerun(input(5)); !errors.Is(err, domain.ErrTerminalState) {
			t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
		}
	})
}

// TestFailureReasonRoundTrip — причина сбоя сохраняется в снимке и восстанавливается.
func TestFailureReasonRoundTrip(t *testing.T) {
	request := newRequest(t)
	toValidated(t, request)
	if _, err := request.StartProcessing(input(3)); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := request.Fail(inputFail(4, "draft_failed", "черновики упали")); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	restored, err := domain.RehydrateRequest(request.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateRequest: %v", err)
	}
	code, text, ok := restored.FailureReason()
	if !ok || code != "draft_failed" || text != "черновики упали" {
		t.Fatalf("FailureReason = %q/%q/%v, ожидалось draft_failed/черновики упали/true", code, text, ok)
	}
}

// TestFailureReasonEmptyOutsideFailed — вне failed причина не раскрывается.
func TestFailureReasonEmptyOutsideFailed(t *testing.T) {
	request := newRequest(t)
	if code, text, ok := request.FailureReason(); ok || code != "" || text != "" {
		t.Fatalf("FailureReason вне failed = %q/%q/%v", code, text, ok)
	}
}
