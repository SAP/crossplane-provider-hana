package privilege

import (
	"testing"
)

// Test cases for special identifiers with embedded quotes
func Test_parsePrivilegeString_SpecialIdentifiers(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		want          Privilege
		wantErr       bool
		wantFormatted string
	}{
		{
			name: "SchemaPrivilegeWithEmbeddedQuotes",
			in:   `CREATE ANY ON SCHEMA "SCHE""M'A"`,
			want: Privilege{
				Type:       SchemaPrivilegeType,
				Name:       "CREATE ANY",
				Identifier: `"SCHE""M'A"`,
			},
			wantFormatted: `CREATE ANY ON SCHEMA "SCHE""""M'A"`,
		},
		{
			name: "ObjectPrivilegeWithQualifiedSpecialIdentifiers",
			in:   `CREATE ANY ON "SCHE""M'A"."OBJECT"`,
			want: Privilege{
				Type:       ObjectPrivilegeType,
				Name:       "CREATE ANY",
				Identifier: `"SCHE""M'A"."OBJECT"`,
			},
			wantFormatted: `CREATE ANY ON "SCHE""""M'A"."OBJECT"`,
		},
		{
			name: "ObjectPrivilegeWithGrantOption",
			in:   "CREATE ANY ON OBJECT WITH GRANT OPTION",
			want: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "CREATE ANY",
				Identifier:  "defaultschema.OBJECT",
				IsGrantable: true,
			},
			wantFormatted: `CREATE ANY ON "defaultschema"."OBJECT" WITH GRANT OPTION`,
		},
		{
			name: "SchemaPrivilegeWithSpecialChars",
			in:   `SELECT ON SCHEMA "My-Schema.Name"`,
			want: Privilege{
				Type:       SchemaPrivilegeType,
				Name:       "SELECT",
				Identifier: `"My-Schema.Name"`,
			},
			wantFormatted: `SELECT ON SCHEMA "My-Schema.Name"`,
		},
		{
			name: "ObjectPrivilegeWithSpacesInName",
			in:   `INSERT ON "My Table"`,
			want: Privilege{
				Type:       ObjectPrivilegeType,
				Name:       "INSERT",
				Identifier: `defaultschema."My Table"`,
			},
			wantFormatted: `INSERT ON "defaultschema"."My Table"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePrivilegeString(tc.in, "defaultschema")
			if (err != nil) != tc.wantErr {
				t.Fatalf("parsePrivilegeString(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			// Check the parsed structure
			if got.Type != tc.want.Type {
				t.Errorf("Type: got %v, want %v", got.Type, tc.want.Type)
			}
			if got.Name != tc.want.Name {
				t.Errorf("Name: got %q, want %q", got.Name, tc.want.Name)
			}
			if got.Identifier != tc.want.Identifier {
				t.Errorf("Identifier: got %q, want %q", got.Identifier, tc.want.Identifier)
			}
			if got.IsGrantable != tc.want.IsGrantable {
				t.Errorf("IsGrantable: got %v, want %v", got.IsGrantable, tc.want.IsGrantable)
			}

			// Check the formatted output
			formatted := got.String()
			if formatted != tc.wantFormatted {
				t.Errorf("String(): got %q, want %q", formatted, tc.wantFormatted)
			}
		})
	}
}

func Test_FormatPrivilegeStrings_SpecialIdentifiers(t *testing.T) {
	cases := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{
			name: "MixedSimpleAndSpecialIdentifiers",
			input: []string{
				`CREATE ANY ON SCHEMA "SCHE""M'A"`,
				`CREATE ANY ON "SCHE""M'A"."OBJECT"`,
				"CREATE ANY ON OBJECT WITH GRANT OPTION",
				"SELECT ON SCHEMA SIMPLE",
			},
			want: []string{
				`CREATE ANY ON SCHEMA "SCHE""""M'A"`,
				`CREATE ANY ON "SCHE""""M'A"."OBJECT"`,
				`CREATE ANY ON "testschema"."OBJECT" WITH GRANT OPTION`,
				`SELECT ON SCHEMA "SIMPLE"`,
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FormatPrivilegeStrings(tc.input, "testschema")
			if (err != nil) != tc.wantErr {
				t.Fatalf("FormatPrivilegeStrings() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tc.want))
			}

			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
