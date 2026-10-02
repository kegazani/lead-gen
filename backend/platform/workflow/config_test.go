package workflow

import "testing"

// TestConfigWithDefaults проверяет подстановку значений по умолчанию.
func TestConfigWithDefaults(t *testing.T) {
	cfg := Config{Address: " localhost:7233 "}.WithDefaults()
	if cfg.Address != "localhost:7233" {
		t.Fatalf("адрес должен быть обрезан: %q", cfg.Address)
	}
	if cfg.Namespace != DefaultNamespace {
		t.Fatalf("namespace = %q, ожидался %q", cfg.Namespace, DefaultNamespace)
	}
	if cfg.TaskQueue != DefaultTaskQueue {
		t.Fatalf("task queue = %q, ожидался %q", cfg.TaskQueue, DefaultTaskQueue)
	}
	if cfg.Identity != DefaultIdentity {
		t.Fatalf("identity = %q, ожидался %q", cfg.Identity, DefaultIdentity)
	}
}

// TestConfigEnabled проверяет признак «рантайм настроен».
func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("пустой адрес не должен считаться настроенным")
	}
	if !(Config{Address: "localhost:7233"}).Enabled() {
		t.Fatal("непустой адрес должен считаться настроенным")
	}
}

// TestConfigCloud проверяет признак подключения к Temporal Cloud.
func TestConfigCloud(t *testing.T) {
	if !(Config{Address: "x", APIKey: "k"}).Cloud() {
		t.Fatal("заданный ключ доступа должен означать Cloud")
	}
	if (Config{Address: "x"}).Cloud() {
		t.Fatal("без ключа доступа подключение не Cloud")
	}
}
