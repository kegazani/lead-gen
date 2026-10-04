package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// fakeDNS — резолвер для тестов: отдаёт заданные записи и считает обращения.
type fakeDNS struct {
	mx      map[string][]string
	host    map[string][]string
	mxErr   error
	hostErr error
	calls   int
}

func (f *fakeDNS) LookupMX(_ context.Context, domain string) ([]string, error) {
	f.calls++
	if f.mxErr != nil {
		return nil, f.mxErr
	}
	return f.mx[domain], nil
}

func (f *fakeDNS) LookupHost(_ context.Context, domain string) ([]string, error) {
	f.calls++
	if f.hostErr != nil {
		return nil, f.hostErr
	}
	return f.host[domain], nil
}

// fakeVerifier — сервис проверки для тестов: отдаёт заданный ответ и считает
// обращения.
type fakeVerifier struct {
	verdict ports.MailboxVerdict
	err     error
	calls   int
	last    string
}

func (f *fakeVerifier) VerifyMailbox(_ context.Context, address string) (ports.MailboxVerdict, error) {
	f.calls++
	f.last = address
	if f.err != nil {
		return ports.MailboxVerdict{}, f.err
	}
	return f.verdict, nil
}

func verifierFor(outcome ports.MailboxOutcome) *fakeVerifier {
	return &fakeVerifier{verdict: ports.MailboxVerdict{Outcome: outcome, Reason: "ответ сервиса", Source: "provider-x"}}
}

func TestVerifyAddressDeliverable(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	verifier := verifierFor(ports.MailboxAccepted)
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "ivan.petrov@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusDeliverable {
		t.Fatalf("статус = %q, ожидался deliverable", checked.Status)
	}
	if checked.Source != "provider-x" || checked.CheckedAt.IsZero() || checked.Reason == "" {
		t.Fatalf("неполный результат: %+v", checked)
	}
}

func TestVerifyAddressInvalidSyntaxNoExternalCall(t *testing.T) {
	dns := &fakeDNS{}
	verifier := verifierFor(ports.MailboxAccepted)
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "not-an-address"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusInvalid {
		t.Fatalf("статус = %q, ожидался invalid", checked.Status)
	}
	if dns.calls != 0 || verifier.calls != 0 {
		t.Fatalf("неверный синтаксис не должен обращаться к сети: dns=%d verifier=%d", dns.calls, verifier.calls)
	}
}

func TestVerifyAddressCatchAllCapsRisky(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	verifier := verifierFor(ports.MailboxAccepted)
	verifier.verdict.CatchAll = true
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusRisky {
		t.Fatalf("catch-all: статус = %q, ожидался risky (не выше)", checked.Status)
	}
	if !checked.CatchAll {
		t.Fatal("признак catch-all должен быть сохранён")
	}
}

func TestVerifyAddressRole(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxAccepted), Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "info@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusRole {
		t.Fatalf("статус = %q, ожидался role", checked.Status)
	}
}

func TestVerifyAddressDisposableNoProviderCall(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"mailinator.com": {"mx.mailinator.com"}}}
	verifier := verifierFor(ports.MailboxAccepted)
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "someone@mailinator.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusDisposable {
		t.Fatalf("статус = %q, ожидался disposable", checked.Status)
	}
	if verifier.calls != 0 {
		t.Fatalf("одноразовый домен не должен проверяться сервисом: %d", verifier.calls)
	}
}

func TestVerifyAddressDomainNotFound(t *testing.T) {
	dns := &fakeDNS{mxErr: ports.ErrDomainNotFound}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxAccepted), Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusInvalid {
		t.Fatalf("статус = %q, ожидался invalid", checked.Status)
	}
}

func TestVerifyAddressNoMXFallsBackToA(t *testing.T) {
	dns := &fakeDNS{host: map[string][]string{"example.com": {"93.184.216.34"}}}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxAccepted), Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "ivan.petrov@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusDeliverable {
		t.Fatalf("откат на A: статус = %q, ожидался deliverable", checked.Status)
	}
}

func TestVerifyAddressNoMXAndNoA(t *testing.T) {
	dns := &fakeDNS{}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxAccepted), Clock: fakeClock{t: testTime}}

	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusInvalid {
		t.Fatalf("статус = %q, ожидался invalid", checked.Status)
	}
}

func TestVerifyAddressRejectedAndUnknown(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxRejected), Clock: fakeClock{t: testTime}}
	checked, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusInvalid {
		t.Fatalf("отказ: статус = %q, ожидался invalid", checked.Status)
	}

	useCase.Verifier = verifierFor(ports.MailboxUnknown)
	checked, err = useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checked.Status != domain.StatusUnknown {
		t.Fatalf("unknown: статус = %q, ожидался unknown", checked.Status)
	}
}

func TestVerifyAddressVerifierUnavailable(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	verifier := &fakeVerifier{err: ports.ErrVerifierUnavailable}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	if _, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"}); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerificationUnavailable", err)
	}
}

func TestVerifyAddressResolverUnavailable(t *testing.T) {
	dns := &fakeDNS{mxErr: ports.ErrResolverUnavailable}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifierFor(ports.MailboxAccepted), Clock: fakeClock{t: testTime}}

	if _, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"}); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerificationUnavailable", err)
	}
}

func TestVerifyAddressWithoutDependencies(t *testing.T) {
	useCase := &VerifyAddress{Clock: fakeClock{t: testTime}}
	if _, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"}); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerificationUnavailable", err)
	}
}

func TestVerifyAddressRejectsUnknownOutcome(t *testing.T) {
	dns := &fakeDNS{mx: map[string][]string{"example.com": {"mx1.example.com"}}}
	verifier := &fakeVerifier{verdict: ports.MailboxVerdict{Outcome: ports.MailboxOutcome("weird")}}
	useCase := &VerifyAddress{DNS: dns, Verifier: verifier, Clock: fakeClock{t: testTime}}

	if _, err := useCase.Run(context.Background(), VerifyAddressInput{Address: "sales@example.com"}); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("нераспознанный исход: ошибка = %v, ожидалась ErrVerificationUnavailable", err)
	}
}
