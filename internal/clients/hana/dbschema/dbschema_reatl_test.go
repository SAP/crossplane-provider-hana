package dbschema

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/apis/schema/v1alpha1"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/clients/hana"
)

func TestReadSchema_RealConnection(t *testing.T) {
	t.Skipf("for debugging only, requires real connection to HANA DB")
	creds := map[string][]byte{
		"endpoint": []byte("hostonly.example.com"),
		"port":     []byte("443"),
		"username": []byte("MYUSER"),
		"password": []byte("Hana)/CompliantPassword123!"),
	}

	// Create HANA DB connection
	db := hana.New(logging.NewNopLogger())
	ctx := context.Background()
	err := db.Connect(ctx, creds)
	if err != nil {
		t.Fatalf("failed to connect to HANA DB: %v", err)
	}

	// Create dbschema client with the DB connection
	client := New(db)

	params := &v1alpha1.DbSchemaParameters{
		SchemaName: "MYUSER", // needs to be an existing schema
	}

	observation, err := client.Read(ctx, params)
	if err != nil {
		t.Fatalf("failed to read schema: %v", err)
	}

	t.Logf("Observed schema: %+v", observation)
}
