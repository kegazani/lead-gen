package batchlimits_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/batchlimits"
)

// TestDefaultCoversThreshold проверяет, что конфигурация по умолчанию валидна и
// даёт доменные лимиты, позволяющие утвердить не менее 50 однородных черновиков
// за одно решение.
func TestDefaultCoversThreshold(t *testing.T) {
	cfg := batchlimits.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("конфигурация по умолчанию отвергнута: %v", err)
	}

	limits, err := batchlimits.Load(cfg)
	if err != nil {
		t.Fatalf("Load конфигурации по умолчанию: %v", err)
	}
	if limits.MinSize < batchlimits.ThresholdDrafts {
		t.Fatalf("минимальный размер %d ниже порога %d", limits.MinSize, batchlimits.ThresholdDrafts)
	}
	if limits.ShardSize < batchlimits.ThresholdDrafts {
		t.Fatalf("размер шарда %d не позволяет охватить порог %d", limits.ShardSize, batchlimits.ThresholdDrafts)
	}
	if limits.MaxSize < batchlimits.ThresholdDrafts {
		t.Fatalf("лимит окна %d не позволяет охватить порог %d", limits.MaxSize, batchlimits.ThresholdDrafts)
	}
}

// TestDefaultPassesThresholdExactly фиксирует, что значение по умолчанию равно
// порогу, а не произвольно больше: минимальный размер охватывает ровно 50.
func TestDefaultPassesThresholdExactly(t *testing.T) {
	cfg := batchlimits.Default()
	if cfg.MinSize != batchlimits.ThresholdDrafts {
		t.Fatalf("минимальный размер по умолчанию %d, ожидался порог %d", cfg.MinSize, batchlimits.ThresholdDrafts)
	}
}

// TestInvalidConfigRejected проверяет, что конфигурация с минимальным размером
// ниже порога или с несогласованными значениями отвергается типизированной
// ошибкой.
func TestInvalidConfigRejected(t *testing.T) {
	tests := []struct {
		name string
		cfg  batchlimits.Config
	}{
		{
			name: "минимальный размер ниже порога",
			cfg:  batchlimits.Config{MinSize: batchlimits.ThresholdDrafts - 1, ShardSize: 100, MaxSize: 200, WindowLimit: 500},
		},
		{
			name: "минимальный размер больше шарда",
			cfg:  batchlimits.Config{MinSize: 60, ShardSize: 50, MaxSize: 200, WindowLimit: 500},
		},
		{
			name: "размер шарда больше максимального",
			cfg:  batchlimits.Config{MinSize: 50, ShardSize: 300, MaxSize: 200, WindowLimit: 500},
		},
		{
			name: "максимальный размер больше лимита окна",
			cfg:  batchlimits.Config{MinSize: 50, ShardSize: 100, MaxSize: 600, WindowLimit: 500},
		},
		{
			name: "нулевой минимальный размер",
			cfg:  batchlimits.Config{ShardSize: 100, MaxSize: 200, WindowLimit: 500},
		},
		{
			name: "нулевой размер шарда",
			cfg:  batchlimits.Config{MinSize: 50, MaxSize: 200, WindowLimit: 500},
		},
		{
			name: "нулевой максимальный размер",
			cfg:  batchlimits.Config{MinSize: 50, ShardSize: 100, WindowLimit: 500},
		},
		{
			name: "нулевой лимит окна",
			cfg:  batchlimits.Config{MinSize: 50, ShardSize: 100, MaxSize: 200},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.cfg.Validate(); !errors.Is(err, batchlimits.ErrInvalidConfig) {
				t.Fatalf("ожидалась ErrInvalidConfig, получено %v", err)
			}
			if _, err := batchlimits.Load(test.cfg); !errors.Is(err, batchlimits.ErrInvalidConfig) {
				t.Fatalf("Load: ожидалась ErrInvalidConfig, получено %v", err)
			}
		})
	}
}

// TestLimitsMapping фиксирует отображение конфигурации в доменный тип: лимит
// окна конфигурации становится MaxSize домена.
func TestLimitsMapping(t *testing.T) {
	cfg := batchlimits.Config{MinSize: 50, ShardSize: 80, MaxSize: 150, WindowLimit: 400}
	limits := cfg.Limits()
	if limits.MinSize != 50 || limits.ShardSize != 80 || limits.MaxSize != 400 {
		t.Fatalf("отображение лимитов неверно: %+v", limits)
	}
}
