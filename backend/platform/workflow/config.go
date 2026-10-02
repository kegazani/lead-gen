package workflow

import "strings"

const (
	// DefaultNamespace — пространство имён Temporal по умолчанию (dev).
	DefaultNamespace = "default"

	// DefaultTaskQueue — очередь задач по умолчанию.
	DefaultTaskQueue = "lead-gen"

	// DefaultIdentity — идентификатор воркера по умолчанию.
	DefaultIdentity = "lead-gen-worker"
)

// Config — параметры подключения к рантайму Temporal.
//
// Пустой Address означает, что рантайм отключён: клиент не создаётся, воркер не
// поднимается, а заявка остаётся в текущем статусе (поведение при недоступности
// Temporal — заявка не теряет состояние и не переводится).
type Config struct {
	// Address — адрес фронтенда Temporal, host:port. Temporal Cloud — адрес
	// вида "<namespace>.tmprl.cloud:7233"; dev — "localhost:7233".
	Address string

	// Namespace — пространство имён. Пусто — DefaultNamespace.
	Namespace string

	// APIKey — ключ доступа Temporal Cloud. Пусто — подключение без TLS/ключа
	// (локальный сервер).
	APIKey string

	// TaskQueue — очередь задач воркера. Пусто — DefaultTaskQueue.
	TaskQueue string

	// Identity — идентификатор воркера для диагностики. Пусто — DefaultIdentity.
	Identity string
}

// Enabled сообщает, задан ли адрес подключения.
func (c Config) Enabled() bool { return strings.TrimSpace(c.Address) != "" }

// Cloud сообщает, что подключение идёт к Temporal Cloud (задан ключ доступа).
func (c Config) Cloud() bool { return strings.TrimSpace(c.APIKey) != "" }

// WithDefaults возвращает копию с заполненными значениями по умолчанию и
// обрезанными пробелами. Исходная конфигурация не изменяется.
func (c Config) WithDefaults() Config {
	c.Address = strings.TrimSpace(c.Address)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if strings.TrimSpace(c.Namespace) == "" {
		c.Namespace = DefaultNamespace
	}
	if strings.TrimSpace(c.TaskQueue) == "" {
		c.TaskQueue = DefaultTaskQueue
	}
	if strings.TrimSpace(c.Identity) == "" {
		c.Identity = DefaultIdentity
	}
	return c
}
