package utils

import "strings"

func EscapeSingleQuotes(input string) string {
	return strings.ReplaceAll(input, "'", "''")
}

func EscapeDoubleQuotes(input string) string {
	return strings.ReplaceAll(input, `"`, `""`)
}

func MapKeysToUpper[A comparable](input map[string]A) map[string]A {
	upperMap := make(map[string]A, len(input))
	for k, v := range input {
		upperMap[strings.ToUpper(k)] = v
	}
	return upperMap
}

func MapsEqual[A comparable](map1, map2 map[string]A) bool {
	isEqual, _, _ := MapsBothDiff(map1, map2)
	return isEqual
}

func ArrayToUpper(arr []string) []string {
	upperArr := make([]string, len(arr))
	for i, v := range arr {
		upperArr[i] = strings.ToUpper(v)
	}
	return upperArr
}

func ArraysEqual[A comparable](arr1, arr2 []A) bool {
	isEqual, _, _ := ArraysBothDiff(arr1, arr2)
	return isEqual
}

func ArraysBothDiff[A comparable](arr1, arr2 []A) (isEqual bool, onlyInArr1 []A, onlyInArr2 []A) {
	set1 := arrayToSet(arr1)
	set2 := arrayToSet(arr2)
	leftDifference := MapDiff(set1, set2)
	if len(leftDifference) != 0 || len(set1) != len(set2) {
		return false, nil, nil
	}
	rightDifference := MapDiff(set2, set1)
	leftArray := setToArray(leftDifference)
	rightArray := setToArray(rightDifference)
	return true, leftArray, rightArray
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
