// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

const contributionProtocol = "gogs-contribution-deadline"

func init() {
	// Register once: the legacy driver's registry is not safe for concurrent writes.
	mysql.RegisterDial(contributionProtocol, func(address string) (net.Conn, error) {
		host, envelope, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrContributionInvalid
		}
		parts := strings.Split(envelope, "~")
		if len(parts) != 2 {
			return nil, ErrContributionInvalid
		}
		nanos, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return nil, ErrContributionInvalid
		}
		return (&contributionDialer{deadline: time.Unix(0, nanos)}).Dial("tcp", net.JoinHostPort(host, parts[0]))
	})
}

// These old drivers do not implement context cancellation. Every socket operation,
// including TLS and driver deadline resets, instead shares one absolute budget.
type contributionConn struct {
	net.Conn
	deadline time.Time
}

func (c *contributionConn) clamp(t time.Time) time.Time {
	if t.IsZero() || t.After(c.deadline) {
		return c.deadline
	}
	return t
}
func (c *contributionConn) SetDeadline(t time.Time) error { return c.Conn.SetDeadline(c.clamp(t)) }
func (c *contributionConn) SetReadDeadline(t time.Time) error {
	return c.Conn.SetReadDeadline(c.clamp(t))
}
func (c *contributionConn) SetWriteDeadline(t time.Time) error {
	return c.Conn.SetWriteDeadline(c.clamp(t))
}

type contributionDialer struct {
	deadline time.Time
	conn     net.Conn
}

func (d *contributionDialer) Dial(network, address string) (net.Conn, error) {
	if !time.Now().Before(d.deadline) {
		return nil, context.DeadlineExceeded
	}
	conn, err := (&net.Dialer{Deadline: d.deadline}).Dial(network, address)
	if err != nil {
		return nil, err
	}
	bounded := &contributionConn{conn, d.deadline}
	if err := bounded.SetDeadline(d.deadline); err != nil {
		conn.Close()
		return nil, err
	}
	d.conn = bounded
	return bounded, nil
}
func (d *contributionDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	if until := time.Now().Add(timeout); timeout > 0 && until.Before(d.deadline) {
		d.deadline = until
	}
	return d.Dial(network, address)
}

type contributionConnector struct {
	backend, dsn string
	deadline     time.Time
}

func (c contributionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !time.Now().Before(c.deadline) {
		return nil, context.DeadlineExceeded
	}
	if c.backend == "mysql" {
		return (&mysql.MySQLDriver{}).Open(c.dsn)
	}
	dialer := &contributionDialer{deadline: c.deadline}
	conn, err := pq.DialOpen(dialer, c.dsn)
	// The pinned pq version does not close a socket when startup panics.
	if err != nil && dialer.conn != nil {
		dialer.conn.Close()
	}
	return conn, err
}
func (c contributionConnector) Driver() driver.Driver { return contributionDriver{c} }

type contributionDriver struct{ connector contributionConnector }

func (d contributionDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

// Each network enqueue owns its connection. Retries retain this exact deadline;
// cancellation without a deadline is bounded by five seconds, not instantaneous.
func contributionDatabase(ctx context.Context) (*sql.DB, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if DbCfg.Type == "sqlite3" {
		return x.DB().DB, false, nil
	}
	deadline := time.Now().Add(5 * time.Second)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	network, address := "tcp", DbCfg.Host
	if DbCfg.Type == "mysql" {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, false, ErrContributionInvalid
		}
		network = contributionProtocol
		address = net.JoinHostPort(host, port+"~"+strconv.FormatInt(deadline.UnixNano(), 10))
	}
	dsn, err := databaseConnectionString(network, address)
	if err != nil {
		return nil, false, ErrContributionInvalid
	}
	db := sql.OpenDB(contributionConnector{DbCfg.Type, dsn, deadline})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, true, nil
}
