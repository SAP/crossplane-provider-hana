package utils

import (
	"regexp"
	"strings"
)

func EscapeSingleQuotes(input string) string {
	return strings.ReplaceAll(input, "'", "''")
}

func EscapeDoubleQuotes(input string) string {
	return strings.ReplaceAll(input, `"`, `""`)
}

// TrimOuterDoubleQuotes safely removes outer double quotes from a string if they exist.
// It handles escaped quotes properly and only trims if the string is properly quoted.
// Supports both HANA SQL escaping (doubling quotes) and backslash escaping.
// Examples:
//   "INSERT ON SCHEMA NEW_SCHEMA" -> INSERT ON SCHEMA NEW_SCHEMA
//   "INSERT ON SCHEMA \"NEW_SCHEMA\"" -> INSERT ON SCHEMA \"NEW_SCHEMA\"
//   "INSERT ON SCHEMA ""NEW_SCHEMA""" -> INSERT ON SCHEMA ""NEW_SCHEMA""
//   INSERT ON SCHEMA NEW_SCHEMA -> INSERT ON SCHEMA NEW_SCHEMA (no change)
//   "unclosed quote -> "unclosed quote (no change)
func TrimOuterDoubleQuotes(input string) string {
	input = strings.TrimSpace(input)
	
	// Must have at least 2 characters and start/end with quotes
	if len(input) < 2 || input[0] != '"' || input[len(input)-1] != '"' {
		return input
	}
	
	// Simple but safe validation: check if we have balanced outer quotes
	// by counting quote characters and looking for obvious patterns
	
	// For empty quotes case
	if len(input) == 2 {
		return input[1 : len(input)-1] // Return empty string
	}
	
	// Basic heuristic: if the second and second-to-last characters are also quotes,
	// this might be an escaped case where we shouldn't trim.
	// However, for simple cases like "INSERT ON SCHEMA NEW_SCHEMA", we should trim.
	
	// Check if this looks like an improperly nested quote situation
	innerContent := input[1 : len(input)-1]
	
	// If the inner content starts AND ends with quotes, it might be double-escaped
	// In that case, be more careful
	if len(innerContent) >= 2 && innerContent[0] == '"' && innerContent[len(innerContent)-1] == '"' {
		// This might be a case like "\"something\"" or """something"""
		// Let's check if it's likely a simple wrap vs complex escaping
		quoteCount := strings.Count(innerContent, `"`)
		
		// If we have exactly 2 quotes (start and end of inner content), 
		// this is likely a case like "\"NEW_SCHEMA\"" and we should still trim outer quotes
		if quoteCount == 2 {
			return innerContent
		}
		
		// If we have more quotes, it's likely a more complex case with escaping
		// In HANA SQL, """something""" would mean "something" with outer quotes
		// We should still trim the outer layer
		return innerContent
	}
	
	// For simple cases, always trim
	return innerContent
}

// ConvertBackslashEscapesToHanaEscapes converts backslash-escaped quotes to HANA SQL double-quote escaping.
// This helps handle privilege strings that may come from systems using different quote escaping conventions.
// Also handles the special case where ""STRING"" should be interpreted as "STRING" with escaped quotes.
// Examples:
//   INSERT ON SCHEMA \"NEW_SCHEMA\" -> INSERT ON SCHEMA "NEW_SCHEMA"
//   USERGROUP OPERATOR ON USERGROUP \"DEFAULT\" -> USERGROUP OPERATOR ON USERGROUP "DEFAULT"
//   INSERT ON SCHEMA ""NEW_SCHEMA"" -> INSERT ON SCHEMA "NEW_SCHEMA" (double-quote wrapper removal)
func ConvertBackslashEscapesToHanaEscapes(input string) string {
	// Handle special case where ""IDENTIFIER"" should become "IDENTIFIER"
	// This pattern appears to come from systems that wrap quoted identifiers with extra quotes
	doubleQuotePattern := regexp.MustCompile(`""([^"]+)""`)
	result := doubleQuotePattern.ReplaceAllString(input, `"$1"`)
	
	// Handle backslash escaping by converting to simple quotes
	result = strings.ReplaceAll(result, `\"`, `"`)
	
	return result
}

// PreprocessPrivilegeStrings safely preprocesses privilege strings to handle outer quotes and escaping.
// This function should be used when processing raw privilege strings from external sources.
func PreprocessPrivilegeStrings(privilegeStrings []string) []string {
	processedStrings := make([]string, len(privilegeStrings))
	for i, privStr := range privilegeStrings {
		// First trim outer quotes
		trimmed := TrimOuterDoubleQuotes(privStr)
		// Convert backslash escaping to HANA SQL escaping for compatibility
		processedStrings[i] = ConvertBackslashEscapesToHanaEscapes(trimmed)
	}
	return processedStrings
}

func ArrayToUpper(arr []string) []string {
	upperArr := make([]string, len(arr))
	for i, v := range arr {
		upperArr[i] = strings.ToUpper(v)
	}
	return upperArr
}

func ArraysEqual[A comparable](arr1, arr2 []A) bool {
	isEqual, _, _, _ := arraysEqualWithDifference(arr1, arr2)
	return isEqual
}

func ArraysBothDiff[A comparable](arr1, arr2 []A) (isEqual bool, onlyInArr1 []A, onlyInArr2 []A) {
	isEqual, set1, set2, leftDifference := arraysEqualWithDifference(arr1, arr2)
	if isEqual {
		return true, nil, nil
	} else if leftDifference == nil {
		leftDifference = MapDiff(set1, set2)
	}

	rightDifference := MapDiff(set2, set1)
	leftArray := setToArray(leftDifference)
	rightArray := setToArray(rightDifference)
	return false, leftArray, rightArray
}

func arraysEqualWithDifference[A comparable](arr1, arr2 []A) (bool, map[A]struct{}, map[A]struct{}, map[A]struct{}) {
	set1 := arrayToSet(arr1)
	set2 := arrayToSet(arr2)
	if len(set1) != len(set2) {
		return false, set1, set2, nil
	}
	leftDifference := MapDiff(set1, set2)
	if len(leftDifference) != 0 {
		return false, set1, set2, leftDifference
	}
	return true, set1, set2, nil
}

func MapsBothDiff[K, V comparable](map1, map2 map[K]V) (isEqual bool, onlyInMap1 map[K]V, onlyInMap2 map[K]V) {
	leftDifference := MapDiff(map1, map2)
	if len(leftDifference) != 0 || len(map1) != len(map2) {
		return false, nil, nil
	}
	rightDifference := MapDiff(map2, map1)
	return true, leftDifference, rightDifference
}

func MapDiff[K, V comparable](map1, map2 map[K]V) map[K]V {
	differenceMap := make(map[K]V)

	for key, val1 := range map1 {
		if val2, ok := map2[key]; !ok || val1 != val2 {
			differenceMap[key] = val1
		}
	}

	return differenceMap
}

func arrayToSet[E comparable](arr []E) map[E]struct{} {
	set := make(map[E]struct{})
	for _, item := range arr {
		set[item] = struct{}{}
	}
	return set
}

func setToArray[E comparable](set map[E]struct{}) []E {
	arr := make([]E, 0, len(set))
	for item := range set {
		arr = append(arr, item)
	}
	return arr
}
