package utils

import "testing"

func TestTrimOuterDoubleQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic quoted string",
			input:    `"INSERT ON SCHEMA NEW_SCHEMA"`,
			expected: `INSERT ON SCHEMA NEW_SCHEMA`,
		},
		{
			name:     "quoted string with inner quotes",
			input:    `"INSERT ON SCHEMA \"NEW_SCHEMA\""`,
			expected: `INSERT ON SCHEMA \"NEW_SCHEMA\"`,
		},
		{
			name:     "unquoted string",
			input:    `INSERT ON SCHEMA NEW_SCHEMA`,
			expected: `INSERT ON SCHEMA NEW_SCHEMA`,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: ``,
		},
		{
			name:     "single quote",
			input:    `"`,
			expected: `"`,
		},
		{
			name:     "unclosed quote",
			input:    `"unclosed quote`,
			expected: `"unclosed quote`,
		},
		{
			name:     "double escaped quotes - should trim",
			input:    `"test ""quoted"" string"`,
			expected: `test ""quoted"" string`,
		},
		{
			name:     "whitespace around quoted string",
			input:    `  "INSERT ON SCHEMA NEW_SCHEMA"  `,
			expected: `INSERT ON SCHEMA NEW_SCHEMA`,
		},
		{
			name:     "string with only quotes",
			input:    `""`,
			expected: ``,
		},
		{
			name:     "mixed quotes - single inside double",
			input:    `"test 'quoted' string"`,
			expected: `test 'quoted' string`,
		},
		{
			name:     "usergroup operator privilege",
			input:    `"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			expected: `USERGROUP OPERATOR ON USERGROUP DEFAULT`,
		},
		{
			name:     "complex schema with quotes",
			input:    `"INSERT ON SCHEMA \"MY SPECIAL SCHEMA\""`,
			expected: `INSERT ON SCHEMA \"MY SPECIAL SCHEMA\"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TrimOuterDoubleQuotes(tt.input)
			if result != tt.expected {
				t.Errorf("TrimOuterDoubleQuotes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEscapeDoubleQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple string with quotes",
			input:    `test "quoted" string`,
			expected: `test ""quoted"" string`,
		},
		{
			name:     "string without quotes",
			input:    `test string`,
			expected: `test string`,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: ``,
		},
		{
			name:     "only quotes",
			input:    `"`,
			expected: `""`,
		},
		{
			name:     "multiple quotes",
			input:    `"test" "more" "quotes"`,
			expected: `""test"" ""more"" ""quotes""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EscapeDoubleQuotes(tt.input)
			if result != tt.expected {
				t.Errorf("EscapeDoubleQuotes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConvertBackslashEscapesToHanaEscapes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple backslash escaped quotes",
			input:    `INSERT ON SCHEMA \"NEW_SCHEMA\"`,
			expected: `INSERT ON SCHEMA "NEW_SCHEMA"`,
		},
		{
			name:     "usergroup with backslash escaped quotes",
			input:    `USERGROUP OPERATOR ON USERGROUP \"DEFAULT\"`,
			expected: `USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
		},
		{
			name:     "no backslash escapes",
			input:    `INSERT ON SCHEMA NEW_SCHEMA`,
			expected: `INSERT ON SCHEMA NEW_SCHEMA`,
		},
		{
			name:     "double-quote wrapper removal",
			input:    `INSERT ON SCHEMA ""NEW_SCHEMA""`,
			expected: `INSERT ON SCHEMA "NEW_SCHEMA"`,
		},
		{
			name:     "already properly escaped for HANA",
			input:    `INSERT ON SCHEMA "NEW_SCHEMA"`,
			expected: `INSERT ON SCHEMA "NEW_SCHEMA"`,
		},
		{
			name:     "mixed escaping",
			input:    `INSERT ON SCHEMA \"MY\"\"SCHEMA\"`,
			expected: `INSERT ON SCHEMA "MY""SCHEMA"`,
		},
		{
			name:     "empty string",
			input:    ``,
			expected: ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertBackslashEscapesToHanaEscapes(tt.input)
			if result != tt.expected {
				t.Errorf("ConvertBackslashEscapesToHanaEscapes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPreprocessPrivilegeStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "basic quoted privileges",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA NEW_SCHEMA`,
				`USERGROUP OPERATOR ON USERGROUP DEFAULT`,
			},
		},
		{
			name: "mixed quoted and unquoted",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`SELECT ON SCHEMA OLD_SCHEMA`,
			},
			expected: []string{
				`INSERT ON SCHEMA NEW_SCHEMA`,
				`SELECT ON SCHEMA OLD_SCHEMA`,
			},
		},
		{
			name: "backslash escaped quotes",
			input: []string{
				`"INSERT ON SCHEMA \"NEW_SCHEMA\""`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
			},
		},
		{
			name: "double-quote wrapper",
			input: []string{
				`"INSERT ON SCHEMA ""NEW_SCHEMA"""`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PreprocessPrivilegeStrings(tt.input)
			for i, expected := range tt.expected {
				if result[i] != expected {
					t.Errorf("PreprocessPrivilegeStrings()[%d] = %q, want %q", i, result[i], expected)
				}
			}
		})
	}
}
