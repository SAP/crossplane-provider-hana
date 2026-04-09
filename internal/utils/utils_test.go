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

func TestMapDiffOnlyDesired(t *testing.T) {
	tests := []struct {
		name     string
		observed map[string]string
		desired  map[string]string
		expected map[string]string
	}{
		{
			name: "no differences - all desired keys match",
			observed: map[string]string{
				"param1": "value1",
				"param2": "value2",
				"param3": "default3", // extra observed parameter (HANA default)
			},
			desired: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
			expected: map[string]string{},
		},
		{
			name: "desired parameter differs from observed",
			observed: map[string]string{
				"param1": "value1",
				"param2": "oldValue",
				"param3": "default3",
			},
			desired: map[string]string{
				"param1": "value1",
				"param2": "newValue",
			},
			expected: map[string]string{
				"param2": "newValue",
			},
		},
		{
			name: "desired parameter missing in observed",
			observed: map[string]string{
				"param1": "value1",
			},
			desired: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
			expected: map[string]string{
				"param2": "value2",
			},
		},
		{
			name: "empty observed map",
			observed: map[string]string{},
			desired: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
			expected: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
		},
		{
			name:     "empty desired map",
			observed: map[string]string{
				"param1": "value1",
				"param2": "value2",
			},
			desired:  map[string]string{},
			expected: map[string]string{},
		},
		{
			name: "observed has many defaults, desired has few",
			observed: map[string]string{
				"max_connections":      "100",
				"timeout":              "30",
				"buffer_size":          "1024",
				"enable_logging":       "true",
				"default_schema":       "SYS",
				"user_defined_param1":  "custom1",
			},
			desired: map[string]string{
				"user_defined_param1": "custom1",
			},
			expected: map[string]string{},
		},
		{
			name: "multiple differences",
			observed: map[string]string{
				"param1": "value1",
				"param2": "value2",
				"param3": "value3",
			},
			desired: map[string]string{
				"param1": "newValue1",
				"param2": "value2",
				"param4": "newValue4",
			},
			expected: map[string]string{
				"param1": "newValue1",
				"param4": "newValue4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapDiffOnlyDesired(tt.observed, tt.desired)
			if len(result) != len(tt.expected) {
				t.Errorf("MapDiffOnlyDesired() returned %d items, want %d", len(result), len(tt.expected))
			}
			for key, expectedVal := range tt.expected {
				if resultVal, ok := result[key]; !ok {
					t.Errorf("MapDiffOnlyDesired() missing key %q", key)
				} else if resultVal != expectedVal {
					t.Errorf("MapDiffOnlyDesired()[%q] = %q, want %q", key, resultVal, expectedVal)
				}
			}
		})
	}
}
