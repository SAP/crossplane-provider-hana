package hana

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"sync"

	// Blank import as specified by the driver
	_ "github.com/SAP/go-hdb/driver"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"golang.org/x/crypto/argon2"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

type hanaDB struct {
	dbs    sync.Map
	logger logging.Logger
	salt   []byte
}

// New returns a new Connector backed by a pool of HANA connections.
func New(logger logging.Logger) xsql.Connector {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return &hanaDB{
		dbs:    sync.Map{},
		logger: logger,
		salt:   salt,
	}
}

func (h *hanaDB) Connect(ctx context.Context, creds map[string][]byte) (xsql.DB, error) {
	endpoint := string(creds[xpv1.ResourceCredentialsSecretEndpointKey])
	port := string(creds[xpv1.ResourceCredentialsSecretPortKey])
	username := string(creds[xpv1.ResourceCredentialsSecretUserKey])
	password := string(creds[xpv1.ResourceCredentialsSecretPasswordKey])
	dsn := DSN(username, password, endpoint, port)

	hashBytes := argon2.IDKey([]byte(dsn), h.salt, 1, 64*1024, 4, 32)
	dsnHash := base64.RawStdEncoding.EncodeToString(hashBytes)

	if val, ok := h.dbs.Load(dsnHash); ok {
		if db, ok := val.(*sql.DB); ok {
			if err := db.PingContext(ctx); err == nil {
				return db, nil
			}
		}
	}

	db, err := sql.Open("hdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open HANA DB connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		go db.Close() // nolint:errcheck
		return nil, fmt.Errorf("failed to ping HANA DB: %w", err)
	}

	prev, loaded := h.dbs.Swap(dsnHash, db)
	if loaded {
		if pdb, ok := prev.(*sql.DB); ok {
			go pdb.Close() // nolint:errcheck
		} else {
			h.logger.Info("Warning: sync.Map loaded value that is not *sql.DB", "type", fmt.Sprintf("%T", prev))
		}
	}

	return db, nil
}

func (h *hanaDB) Disconnect() error {
	var wg sync.WaitGroup

	h.dbs.Range(func(_, val any) bool {
		db, ok := val.(*sql.DB)
		if ok {
			wg.Go(func() {
				_ = db.Close()
			})
		} else {
			h.logger.Info("Warning: sync.Map loaded value that is not *sql.DB", "type", fmt.Sprintf("%T", val))
		}
		return true
	})

	wg.Wait()
	h.dbs.Clear()

	return nil
}

// DSN returns a DSN string for the HANA DB connection
func DSN(username string, password string, endpoint string, port string) string {
	// we need to encode the username and password to handle special characters
	u := &url.URL{
		Scheme:   "hdb",
		User:     url.UserPassword(username, password), // Handles encoding automatically
		Host:     fmt.Sprintf("%s:%s", endpoint, port),
		RawQuery: fmt.Sprintf("TLSServerName=%s", endpoint),
	}
	return u.String()
}

// QueryClient defines the base methods for a query client with typed parameters
// P is the parameters type, O is the observation type
type QueryClient[P any, O any] interface {
	Read(ctx context.Context, parameters *P) (observed *O, err error)
	Create(ctx context.Context, parameters *P) error
	Delete(ctx context.Context, parameters *P) error
}
