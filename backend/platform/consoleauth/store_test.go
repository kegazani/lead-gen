package consoleauth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/platform/consoleauth"
)

// TestMemoryStoreRoundTrip проверяет базовый контракт стора в памяти:
// сохранить, прочитать, удалить, прочитать отсутствующую.
func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := consoleauth.NewMemoryStore()
	at := time.Date(2026, time.February, 3, 9, 0, 0, 0, time.UTC)

	if _, ok, err := store.Load(ctx, "нет-такой"); err != nil || ok {
		t.Fatalf("Load отсутствующей сессии = ok %v, err %v", ok, err)
	}

	rec := consoleauth.Record{ID: "s1", Username: "operator", CSRF: "csrf-1", LastActive: at}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("Load сохранённой сессии = ok %v, err %v", ok, err)
	}
	if got != rec {
		t.Fatalf("Load = %+v, ожидалось %+v", got, rec)
	}

	// Повторный Save по тому же ID заменяет запись.
	rec.CSRF = "csrf-2"
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("повторный Save: %v", err)
	}
	got, _, _ = store.Load(ctx, "s1")
	if got.CSRF != "csrf-2" {
		t.Fatalf("CSRF после перезаписи = %q", got.CSRF)
	}

	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := store.Load(ctx, "s1"); err != nil || ok {
		t.Fatalf("Load после Delete = ok %v, err %v", ok, err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatalf("повторный Delete: %v", err)
	}
}

// TestMemoryStoreConcurrentAccess проверяет потокобезопасность стора: чтения и
// записи идут из нескольких горутин одновременно (ловится под -race).
func TestMemoryStoreConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	store := consoleauth.NewMemoryStore()
	ids := []string{"a", "b", "c", "d"}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = store.Save(ctx, consoleauth.Record{ID: id, Username: "operator", CSRF: id, LastActive: time.Now()})
				_, _, _ = store.Load(ctx, id)
				_, _, _ = store.Load(ctx, "missing")
				_ = store.Delete(ctx, "missing")
			}
		}(id)
	}
	wg.Wait()

	for _, id := range ids {
		if _, ok, err := store.Load(ctx, id); err != nil || !ok {
			t.Fatalf("сессия %q потеряна после конкурентного доступа: ok %v, err %v", id, ok, err)
		}
	}
}

// TestManagerUsesInjectedStore проверяет, что менеджер пишет сессии именно во
// впрыснутый стор: запись появляется после Login и исчезает после Logout.
func TestManagerUsesInjectedStore(t *testing.T) {
	ctx := context.Background()
	store := consoleauth.NewMemoryStore()
	mgr := consoleauth.New(consoleauth.Config{Username: "operator", Password: "secret", Store: store})

	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	rec, ok, err := store.Load(ctx, id)
	if err != nil || !ok {
		t.Fatalf("сессия не попала во впрыснутый стор: ok %v, err %v", ok, err)
	}
	if rec.Username != "operator" || rec.CSRF == "" {
		t.Fatalf("запись в сторе = %+v", rec)
	}

	sess, ok := mgr.Authenticate(id)
	if !ok || sess.Username() != "operator" {
		t.Fatalf("Authenticate через стор = ok %v, логин %q", ok, sessionUsername(sess))
	}

	mgr.Logout(id)
	if _, ok, _ := store.Load(ctx, id); ok {
		t.Fatalf("Logout не удалил сессию из стора")
	}
}

// sessionUsername возвращает логин сессии или пусто, если сессии нет (nil).
func sessionUsername(sess *consoleauth.Session) string {
	if sess == nil {
		return ""
	}
	return sess.Username()
}

// failingStore — стор, все операции которого завершаются ошибкой. Нужен, чтобы
// проверить отказоустойчивое поведение менеджера при недоступном хранилище.
type failingStore struct{ err error }

func (f failingStore) Save(context.Context, consoleauth.Record) error { return f.err }
func (f failingStore) Load(context.Context, string) (consoleauth.Record, bool, error) {
	return consoleauth.Record{}, false, f.err
}
func (f failingStore) Delete(context.Context, string) error { return f.err }

// TestManagerFailsClosedOnStoreError проверяет, что ошибка хранилища не выдаёт
// сессию: Login возвращает ошибку, Authenticate — отказ, Logout не паникует.
func TestManagerFailsClosedOnStoreError(t *testing.T) {
	boom := errors.New("хранилище недоступно")
	store := failingStore{err: boom}
	mgr := consoleauth.New(consoleauth.Config{Username: "operator", Password: "secret", Store: store})

	if _, err := mgr.Login("operator", "secret"); !errors.Is(err, boom) {
		t.Fatalf("Login при сбое стора: err = %v, ожидалась ошибка хранилища", err)
	}
	if _, ok := mgr.Authenticate("любой"); ok {
		t.Fatalf("Authenticate при сбое стора = true")
	}
	mgr.Logout("любой") // не паникует
}

// TestSharedStoreAcceptsSessionAcrossServers — доказательство общего входа: два
// HTTP-сервера консоли используют ОДИН стор. Сессия, выданная входом на первом
// сервере, принимается вторым без повторного входа, хотя у второго другие
// учётные данные — проверка идёт только по сессии в общем сторе.
func TestSharedStoreAcceptsSessionAcrossServers(t *testing.T) {
	store := consoleauth.NewMemoryStore()
	serverA := httptest.NewServer(consoleServer(consoleauth.New(consoleauth.Config{
		Username: "op-a", Password: "secret-a", Store: store,
	})))
	defer serverA.Close()
	serverB := httptest.NewServer(consoleServer(consoleauth.New(consoleauth.Config{
		Username: "op-b", Password: "secret-b", Store: store,
	})))
	defer serverB.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Вход на первом сервере.
	resp, err := client.PostForm(serverA.URL+"/login", url.Values{
		"username": {"op-a"}, "password": {"secret-a"},
	})
	if err != nil {
		t.Fatalf("вход на первом сервере: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("вход на первом сервере = %d, ожидался 303", resp.StatusCode)
	}
	var session *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == consoleauth.CookieName {
			session = cookie
		}
	}
	if session == nil {
		t.Fatalf("первый сервер не выдал cookie %q", consoleauth.CookieName)
	}

	// Тот же cookie принимается вторым сервером: повторного входа нет.
	req, err := http.NewRequest(http.MethodGet, serverB.URL+"/", nil)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: session.Value})
	respB, err := client.Do(req)
	if err != nil {
		t.Fatalf("запрос на второй сервер: %v", err)
	}
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusOK {
		t.Fatalf("второй сервер со сессией первого = %d, ожидался 200", respB.StatusCode)
	}
	body, _ := io.ReadAll(respB.Body)
	if !strings.Contains(string(body), "op-a") {
		t.Fatalf("второй сервер не увидел логин сессии первого: %q", string(body))
	}

	// Отзыв в общем сторе: выход на втором сервере делает сессию недействительной
	// и для первого.
	logoutReq, _ := http.NewRequest(http.MethodGet, serverB.URL+"/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: session.Value})
	logoutResp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("выход на втором сервере: %v", err)
	}
	logoutResp.Body.Close()

	recheck, _ := http.NewRequest(http.MethodGet, serverA.URL+"/", nil)
	recheck.AddCookie(&http.Cookie{Name: consoleauth.CookieName, Value: session.Value})
	recheckResp, err := client.Do(recheck)
	if err != nil {
		t.Fatalf("повторный запрос на первый сервер: %v", err)
	}
	defer recheckResp.Body.Close()
	if recheckResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("после отзыва в общем сторе первый сервер = %d, ожидался 303", recheckResp.StatusCode)
	}
}

// consoleServer собирает минимальный HTTP-сервер консоли поверх менеджера:
// /login выдаёт сессию, остальные пути требуют её и отвечают логином оператора.
func consoleServer(mgr *consoleauth.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "разбор формы", http.StatusBadRequest)
			return
		}
		id, err := mgr.Login(r.FormValue("username"), r.FormValue("password"))
		if err != nil {
			http.Error(w, "отказ", http.StatusUnauthorized)
			return
		}
		mgr.SetSessionCookie(w, r, id)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(consoleauth.CookieName); err == nil {
			mgr.Logout(cookie.Value)
		}
		mgr.ClearSessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
	protected := mgr.Middleware(consoleauth.Policy{
		LoginPath: "/login",
		Public:    []string{"/login", "/logout"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess := consoleauth.Current(r.Context()); sess != nil {
			_, _ = w.Write([]byte(sess.Username()))
		}
	}))
	mux.Handle("/", protected)
	return mux
}
