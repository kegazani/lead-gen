package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// Ledger — PostgreSQL-реализация ports.SignalLedger: журнал обработанных сырых
// сигналов для дедупликации входящих webhook. Инвариант владельца: входящий
// сигнал дедуплицируется до квалификации и в той же транзакции, что и запись
// события, поэтому журнал участвует в транзакции unit-of-work.
type Ledger struct {
	db execer
}

// Ledger реализует порт журнала дедупликации сигналов.
var _ ports.SignalLedger = (*Ledger)(nil)

// NewLedger создаёт журнал поверх пула соединений.
func NewLedger(pool *pgxpool.Pool) *Ledger {
	if pool == nil {
		panic("postgres: NewLedger с nil-пулом")
	}
	return &Ledger{db: pool}
}

// newLedgerTx создаёт журнал, привязанный к активной транзакции.
func newLedgerTx(tx execer) *Ledger {
	return &Ledger{db: tx}
}

// Seen сообщает, обрабатывался ли сигнал ранее.
func (l *Ledger) Seen(ctx context.Context, key domain.SignalID) (bool, error) {
	var seen bool
	if err := l.db.QueryRow(ctx, selectLedgerKey, key.String()).Scan(&seen); err != nil {
		return false, fmt.Errorf("postgres: чтение отметки сигнала %s: %w", key, err)
	}
	return seen, nil
}

// Mark фиксирует сигнал как обработанный. Пустой ключ отвергается: дедуплицировать
// нечем. Повторная отметка того же ключа — no-op.
func (l *Ledger) Mark(ctx context.Context, key domain.SignalID) error {
	if strings.TrimSpace(string(key)) == "" {
		return errors.New("postgres: отметка пустого ключа дедупликации")
	}
	if _, err := l.db.Exec(ctx, insertLedgerKey, key.String()); err != nil {
		return fmt.Errorf("postgres: отметка сигнала %s: %w", key, err)
	}
	return nil
}

// SQL-запросы журнала дедупликации.
const (
	selectLedgerKey = `
SELECT EXISTS (SELECT 1 FROM "engagement-observation".signal_ledger WHERE key = $1)`

	insertLedgerKey = `
INSERT INTO "engagement-observation".signal_ledger (key)
VALUES ($1)
ON CONFLICT (key) DO NOTHING`
)
