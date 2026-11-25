package hana

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

type fakeDatabase struct {
	pingErr     error
	pings       atomic.Int32
	closes      atomic.Int32
	pingStarted chan struct{}
	pingBlock   chan struct{}
	pingOnce    sync.Once
	closed      chan struct{}
	once        sync.Once
}

func (d *fakeDatabase) PingContext(context.Context) error {
	d.pings.Add(1)
	if d.pingStarted != nil {
		d.pingOnce.Do(func() { close(d.pingStarted) })
	}
	if d.pingBlock != nil {
		<-d.pingBlock
	}
	return d.pingErr
}

func (d *fakeDatabase) Close() error {
	d.closes.Add(1)
	d.once.Do(func() { close(d.closed) })
	return nil
}

func (d *fakeDatabase) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}

func (d *fakeDatabase) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func (d *fakeDatabase) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}

func (d *fakeDatabase) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}

type fakeOpener struct {
	mu          sync.Mutex
	dbs         []*fakeDatabase
	pingErr     error
	pingStarted chan struct{}
	pingBlock   chan struct{}
}

func (o *fakeOpener) open(string) (database, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	db := &fakeDatabase{
		pingErr:     o.pingErr,
		pingStarted: o.pingStarted,
		pingBlock:   o.pingBlock,
		closed:      make(chan struct{}),
	}
	o.dbs = append(o.dbs, db)
	return db, nil
}

func (o *fakeOpener) opened() []*fakeDatabase {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*fakeDatabase(nil), o.dbs...)
}

func testCredentials(password string) map[string][]byte {
	return map[string][]byte{
		xpv2.CredentialsSecretEndpointKey: []byte("example.hana.ondemand.com"),
		xpv2.CredentialsSecretPortKey:     []byte("443"),
		xpv2.CredentialsSecretUserKey:     []byte("ADMIN"),
		xpv2.CredentialsSecretPasswordKey: []byte(password),
	}
}

func TestConnectReusesPool(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)

	first, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() first call: %v", err)
	}
	second, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() second call: %v", err)
	}

	dbs := opener.opened()
	if len(dbs) != 1 {
		t.Fatalf("opened %d pools, want 1", len(dbs))
	}
	if got := dbs[0].pings.Load(); got != 1 {
		t.Fatalf("PingContext() called %d times, want 1", got)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() first lease: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close() second lease: %v", err)
	}
	if err := connector.Disconnect(); err != nil {
		t.Fatalf("Disconnect(): %v", err)
	}
	if got := dbs[0].closes.Load(); got != 1 {
		t.Fatalf("pool closed %d times, want 1", got)
	}
}

func TestConnectConcurrentReuse(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	const count = 32

	pools := make([]xsql.DB, count)
	errs := make(chan error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pool, err := connector.Connect(context.Background(), testCredentials("secret"))
			if err != nil {
				errs <- err
				return
			}
			pools[i] = pool
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Connect(): %v", err)
	}

	if got := len(opener.opened()); got != 1 {
		t.Fatalf("opened %d pools, want 1", got)
	}
	for i, pool := range pools {
		if err := pool.Close(); err != nil {
			t.Errorf("Close() lease %d: %v", i, err)
		}
	}
}

func TestConnectSeparatesRotatedCredentials(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	connector.idleTimeout = 20 * time.Millisecond

	oldPool, err := connector.Connect(context.Background(), testCredentials("old-secret"))
	if err != nil {
		t.Fatalf("Connect() old credentials: %v", err)
	}
	newPool, err := connector.Connect(context.Background(), testCredentials("new-secret"))
	if err != nil {
		t.Fatalf("Connect() new credentials: %v", err)
	}

	dbs := opener.opened()
	if len(dbs) != 2 {
		t.Fatalf("opened %d pools, want 2", len(dbs))
	}
	if got := dbs[0].closes.Load(); got != 0 {
		t.Fatalf("old pool closed immediately %d times, want 0", got)
	}
	if err := oldPool.Close(); err != nil {
		t.Fatalf("Close() old lease: %v", err)
	}
	waitForClose(t, dbs[0])
	if err := newPool.Close(); err != nil {
		t.Fatalf("Close() new lease: %v", err)
	}
}

func TestConnectRefreshesPoolExpiration(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	connector.idleTimeout = 80 * time.Millisecond

	first, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() first call: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() first lease: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	second, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() second call: %v", err)
	}

	db := opener.opened()[0]
	time.Sleep(50 * time.Millisecond)
	if got := db.closes.Load(); got != 0 {
		t.Fatalf("pool closed %d times before refreshed timeout, want 0", got)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close() second lease: %v", err)
	}
	waitForClose(t, db)
}

func TestConnectKeepsPoolWhileLeased(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	connector.idleTimeout = 20 * time.Millisecond

	first, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() first call: %v", err)
	}
	second, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() second call: %v", err)
	}
	db := opener.opened()[0]

	if err := first.Close(); err != nil {
		t.Fatalf("Close() first lease: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() first lease again: %v", err)
	}
	time.Sleep(2 * connector.idleTimeout)
	if got := db.closes.Load(); got != 0 {
		t.Fatalf("pool closed %d times with an active lease, want 0", got)
	}

	if err := second.Close(); err != nil {
		t.Fatalf("Close() second lease: %v", err)
	}
	waitForClose(t, db)
}

func TestConnectClosesPoolAfterFailedPing(t *testing.T) {
	errPing := errors.New("ping failed")
	opener := &fakeOpener{pingErr: errPing}
	connector := newConnector(logging.NewNopLogger(), opener.open)

	if _, err := connector.Connect(context.Background(), testCredentials("secret")); !errors.Is(err, errPing) {
		t.Fatalf("Connect() error = %v, want %v", err, errPing)
	}
	dbs := opener.opened()
	if len(dbs) != 1 {
		t.Fatalf("opened %d pools, want 1", len(dbs))
	}
	if got := dbs[0].closes.Load(); got != 1 {
		t.Fatalf("failed pool closed %d times, want 1", got)
	}
}

func TestConnectClosesIdlePool(t *testing.T) {
	opener := &fakeOpener{}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	connector.idleTimeout = time.Millisecond

	first, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect(): %v", err)
	}
	db := opener.opened()[0]
	if err := first.Close(); err != nil {
		t.Fatalf("Close() lease: %v", err)
	}
	waitForClose(t, db)
	if got := db.closes.Load(); got != 1 {
		t.Fatalf("idle pool closed %d times, want 1", got)
	}

	second, err := connector.Connect(context.Background(), testCredentials("secret"))
	if err != nil {
		t.Fatalf("Connect() after expiration: %v", err)
	}
	if got := len(opener.opened()); got != 2 {
		t.Fatalf("opened %d pools after expiration, want 2", got)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close() second lease: %v", err)
	}
}

func TestSlowConnectDoesNotHoldLifecycleLock(t *testing.T) {
	pingStarted := make(chan struct{})
	pingBlock := make(chan struct{})
	opener := &fakeOpener{pingStarted: pingStarted, pingBlock: pingBlock}
	connector := newConnector(logging.NewNopLogger(), opener.open)
	connectDone := make(chan error, 1)
	disconnectDone := make(chan error, 1)

	go func() {
		_, err := connector.Connect(context.Background(), testCredentials("secret"))
		connectDone <- err
	}()
	<-pingStarted
	go func() {
		disconnectDone <- connector.Disconnect()
	}()

	for {
		connector.lifecycleMu.RLock()
		closed := connector.closed
		connector.lifecycleMu.RUnlock()
		if closed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := connector.Connect(context.Background(), testCredentials("secret")); !errors.Is(err, errConnectorClosed) {
		t.Fatalf("Connect() during shutdown error = %v, want %v", err, errConnectorClosed)
	}

	close(pingBlock)
	if err := <-connectDone; err != nil {
		t.Fatalf("initial Connect(): %v", err)
	}
	if err := <-disconnectDone; err != nil {
		t.Fatalf("Disconnect(): %v", err)
	}
	waitForClose(t, opener.opened()[0])
}

func TestDisconnectRejectsNewConnections(t *testing.T) {
	connector := newConnector(logging.NewNopLogger(), (&fakeOpener{}).open)
	if err := connector.Disconnect(); err != nil {
		t.Fatalf("Disconnect(): %v", err)
	}
	if _, err := connector.Connect(context.Background(), testCredentials("secret")); !errors.Is(err, errConnectorClosed) {
		t.Fatalf("Connect() error = %v, want %v", err, errConnectorClosed)
	}
}

func waitForClose(t *testing.T, db *fakeDatabase) {
	t.Helper()
	select {
	case <-db.closed:
	case <-time.After(time.Second):
		t.Fatal("pool was not closed")
	}
}
