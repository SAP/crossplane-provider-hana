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
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"golang.org/x/crypto/argon2"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

type hanaDB struct {
	*sql.DB
	dbs      sync.Map
	endpoint string
	port     string
	logger   logging.Logger
	salt     []byte
}

// New returns a new DB client with the provided logger
func New(logger logging.Logger) xsql.DB {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return &hanaDB{
		dbs:    sync.Map{},
		logger: logger,
		salt:   salt,
	}
}

func (h *hanaDB) Connect(ctx context.Context, creds map[string][]byte) error {
	h.endpoint = string(creds[xpv1.ResourceCredentialsSecretEndpointKey])
	h.port = string(creds[xpv1.ResourceCredentialsSecretPortKey])
	username := string(creds[xpv1.ResourceCredentialsSecretUserKey])
	password := string(creds[xpv1.ResourceCredentialsSecretPasswordKey])
	dsn := DSN(username, password, h.endpoint, h.port)

	hashBytes := argon2.IDKey([]byte(dsn), h.salt, 1, 64*1024, 4, 32)
	dsnHash := base64.RawStdEncoding.EncodeToString(hashBytes)

	if val, ok := h.dbs.Load(dsnHash); ok {
		if db, ok := val.(*sql.DB); ok {
			if err := db.PingContext(ctx); err == nil {
				h.DB = db
				return nil
			}
		}
	}

	db, err := sql.Open("hdb", dsn)
	if err != nil {
		return fmt.Errorf("failed to open HANA DB connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		go db.Close() // nolint:errcheck
		return fmt.Errorf("failed to ping HANA DB: %w", err)
	}

	prev, loaded := h.dbs.Swap(dsnHash, db)
	if loaded {
		if pdb, ok := prev.(*sql.DB); ok {
			go pdb.Close() // nolint:errcheck
		} else {
			h.logger.Info("Warning: sync.Map loaded value that is not *sql.DB", "type", fmt.Sprintf("%T", prev))
		}
	}
	h.DB = db

	return nil
}

func (h *hanaDB) Disconnect() error {
	var wg sync.WaitGroup

	h.dbs.Range(func(_, val any) bool {
		db, ok := val.(*sql.DB)
		if ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = db.Close()
			}()
		} else {
			// Log warning when loaded value is not *sql.DB
			h.logger.Info("Warning: sync.Map loaded value that is not *sql.DB", "type", fmt.Sprintf("%T", val))
		}
		return true
	})

	wg.Wait()

	h.dbs.Clear()
	h.DB = nil

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

// GetConnectionDetails returns the connection details
func (h *hanaDB) GetConnectionDetails(username, password string) managed.ConnectionDetails {
	return managed.ConnectionDetails{
		xpv1.ResourceCredentialsSecretUserKey:     []byte(username),
		xpv1.ResourceCredentialsSecretPasswordKey: []byte(password),
		xpv1.ResourceCredentialsSecretEndpointKey: []byte(h.endpoint),
		xpv1.ResourceCredentialsSecretPortKey:     []byte(h.port),
	}
}

// QueryClient defines the methods for a query client
type QueryClient[P any, O any] interface {
	Read(ctx context.Context, parameters *P) (observed *O, err error)
	Create(ctx context.Context, parameters *P, args ...any) error
	Delete(ctx context.Context, parameters *P) error
}

type UserQueryClient[P any, O any] interface {
	Read(ctx context.Context, parameters *P, password string) (observed *O, err error)
	Create(ctx context.Context, parameters *P, args ...any) error
	Delete(ctx context.Context, parameters *P) error
}
