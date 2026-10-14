package hana

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	// Blank import specified by driver.
	_ "github.com/SAP/go-hdb/driver"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

var errConnectorClosed = errors.New("HANA DB connector is closed")

// Retain unused credential pools briefly so closely spaced reconciliations can reuse them.
const defaultPoolIdleTimeout = 5 * time.Minute

type database interface {
	xsql.DB
	PingContext(context.Context) error
	Close() error
}

type poolSlot struct {
	mu        sync.Mutex
	db        database
	leases    int
	lastUsed  time.Time
	idleTimer *time.Timer
	retired   bool
}

type poolLease struct {
	xsql.DB
	once    sync.Once
	release func()
}

type hanaDB struct {
	dbs         sync.Map
	logger      logging.Logger
	open        func(string) (database, error)
	hmacKey     [sha256.Size]byte
	idleTimeout time.Duration

	lifecycleMu  sync.RWMutex
	closed       bool
	connectWG    sync.WaitGroup
	closeWG      sync.WaitGroup
	shutdownDone chan struct{}
	shutdownErr  error
}

// New returns a new connector backed by shared HANA connection pools.
func New(logger logging.Logger) xsql.Connector {
	return newConnector(logger, func(dsn string) (database, error) {
		return sql.Open("hdb", dsn)
	})
}

func newConnector(logger logging.Logger, open func(string) (database, error)) *hanaDB {
	h := &hanaDB{
		logger:       logger,
		open:         open,
		idleTimeout:  defaultPoolIdleTimeout,
		shutdownDone: make(chan struct{}),
	}
	_, _ = rand.Read(h.hmacKey[:])
	return h
}

// Connect returns the pool selected by the supplied credentials.
func (h *hanaDB) Connect(ctx context.Context, creds map[string][]byte) (xsql.DB, error) {
	h.lifecycleMu.RLock()
	if h.closed {
		h.lifecycleMu.RUnlock()
		return nil, errConnectorClosed
	}
	h.connectWG.Add(1)
	h.lifecycleMu.RUnlock()
	defer h.connectWG.Done()

	endpoint := string(creds[xpv2.CredentialsSecretEndpointKey])
	port := string(creds[xpv2.CredentialsSecretPortKey])
	username := string(creds[xpv2.CredentialsSecretUserKey])
	password := string(creds[xpv2.CredentialsSecretPasswordKey])
	dsn := DSN(username, password, endpoint, port)

	key := h.fingerprint(dsn)
	return h.acquire(ctx, key, dsn)
}

func (h *hanaDB) acquire(ctx context.Context, key [sha256.Size]byte, dsn string) (xsql.DB, error) {
	var slot *poolSlot
	for {
		value, ok := h.dbs.Load(key)
		if !ok {
			value, _ = h.dbs.LoadOrStore(key, &poolSlot{})
		}
		slot = value.(*poolSlot)

		slot.mu.Lock()
		if !slot.retired {
			break
		}
		slot.mu.Unlock()
	}
	defer slot.mu.Unlock()

	if slot.db != nil {
		if slot.idleTimer != nil {
			slot.idleTimer.Stop()
			slot.idleTimer = nil
		}
		slot.leases++
		db := slot.db
		return h.newLease(key, slot, db), nil
	}

	db, err := h.open(dsn)
	if err != nil {
		slot.retired = true
		h.dbs.CompareAndDelete(key, slot)
		return nil, fmt.Errorf("failed to open HANA DB connection: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		slot.retired = true
		h.dbs.CompareAndDelete(key, slot)
		return nil, fmt.Errorf("failed to ping HANA DB: %w", err)
	}

	slot.db = db
	slot.leases = 1
	return h.newLease(key, slot, db), nil
}

func (h *hanaDB) fingerprint(dsn string) [sha256.Size]byte {
	hash := hmac.New(sha256.New, h.hmacKey[:])
	_, _ = hash.Write([]byte(dsn))
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func (h *hanaDB) newLease(key [sha256.Size]byte, slot *poolSlot, db database) xsql.DB {
	return &poolLease{
		DB: db,
		release: func() {
			h.release(key, slot)
		},
	}
}

func (l *poolLease) Close() error {
	l.once.Do(l.release)
	return nil
}

func (h *hanaDB) release(key [sha256.Size]byte, slot *poolSlot) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.leases == 0 {
		return
	}
	slot.leases--
	if slot.leases != 0 || slot.retired || slot.db == nil {
		return
	}

	slot.lastUsed = time.Now()
	slot.idleTimer = time.AfterFunc(h.idleTimeout, func() {
		h.expire(key, slot)
	})
}

func (h *hanaDB) expire(key [sha256.Size]byte, slot *poolSlot) {
	h.lifecycleMu.RLock()
	if h.closed {
		h.lifecycleMu.RUnlock()
		return
	}

	slot.mu.Lock()
	if slot.retired || slot.db == nil || slot.leases != 0 || time.Since(slot.lastUsed) < h.idleTimeout {
		slot.mu.Unlock()
		h.lifecycleMu.RUnlock()
		return
	}
	slot.retired = true
	db := slot.db
	slot.db = nil
	slot.idleTimer = nil
	h.dbs.CompareAndDelete(key, slot)
	slot.mu.Unlock()
	h.closeWG.Add(1)
	h.lifecycleMu.RUnlock()
	defer h.closeWG.Done()
	if err := db.Close(); err != nil {
		h.logger.Info("Cannot close idle HANA DB pool", "error", err)
	}
}

// Disconnect closes every cached pool and rejects new connections.
func (h *hanaDB) Disconnect() error {
	h.lifecycleMu.Lock()
	if h.closed {
		done := h.shutdownDone
		h.lifecycleMu.Unlock()
		<-done
		return h.shutdownErr
	}
	h.closed = true
	h.lifecycleMu.Unlock()

	h.connectWG.Wait()

	h.lifecycleMu.Lock()

	var dbs []database
	h.dbs.Range(func(_, value any) bool {
		slot, ok := value.(*poolSlot)
		if !ok {
			h.logger.Info("Warning: sync.Map loaded value is not *poolSlot", "type", fmt.Sprintf("%T", value))
			return true
		}

		slot.mu.Lock()
		slot.retired = true
		if slot.idleTimer != nil {
			slot.idleTimer.Stop()
			slot.idleTimer = nil
		}
		if slot.db != nil {
			dbs = append(dbs, slot.db)
			slot.db = nil
		}
		slot.mu.Unlock()
		return true
	})
	h.dbs.Clear()
	h.closeWG.Add(len(dbs))
	h.lifecycleMu.Unlock()

	errs := make(chan error, len(dbs))
	var wg sync.WaitGroup
	for _, db := range dbs {
		wg.Go(func() {
			defer h.closeWG.Done()
			errs <- db.Close()
		})
	}
	wg.Wait()
	close(errs)
	h.closeWG.Wait()

	var closeErrs []error
	for err := range errs {
		if err != nil {
			closeErrs = append(closeErrs, err)
		}
	}
	closeErr := errors.Join(closeErrs...)
	h.lifecycleMu.Lock()
	h.shutdownErr = closeErr
	close(h.shutdownDone)
	h.lifecycleMu.Unlock()
	return closeErr
}

// DSN returns a DSN string for a HANA DB connection.
func DSN(username string, password string, endpoint string, port string) string {
	// URL user info encoding handles special characters in credentials.
	u := &url.URL{
		Scheme:   "hdb",
		User:     url.UserPassword(username, password),
		Host:     fmt.Sprintf("%s:%s", endpoint, port),
		RawQuery: fmt.Sprintf("TLSServerName=%s", endpoint),
	}
	return u.String()
}

// QueryClient defines the base methods for a query client with typed parameters.
type QueryClient[P any, O any] interface {
	Read(ctx context.Context, parameters *P) (observed *O, err error)
	Create(ctx context.Context, parameters *P) error
	Delete(ctx context.Context, parameters *P) error
}
