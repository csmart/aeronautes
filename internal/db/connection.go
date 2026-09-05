package db

import (
	"context"
	"database/sql/driver"
	"fmt"
)

// A successful PRAGMA assignment can leave WAL unavailable. Check every
// physical connection before it can acknowledge durable recovery records.
type durableConnector struct{ driver.Connector }

func (c durableConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := verifyDurability(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("verify SQLite durability: %w", err)
	}
	return conn, nil
}

func verifyDurability(ctx context.Context, conn driver.Conn) error {
	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		return fmt.Errorf("SQLite connection cannot query its effective settings")
	}
	rows, err := queryer.QueryContext(ctx,
		`SELECT journal_mode, synchronous FROM pragma_journal_mode(), pragma_synchronous()`, nil)
	if err != nil {
		return err
	}
	values := make([]driver.Value, 2)
	err = rows.Next(values)
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if values[0] != "wal" || values[1] != int64(2) {
		return fmt.Errorf("journal_mode=%v synchronous=%v; require wal and FULL (2)", values[0], values[1])
	}
	return nil
}
