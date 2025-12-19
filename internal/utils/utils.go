package utils

func arrayToSet[E comparable](arr []E) map[E]struct{} {
	set := make(map[E]struct{})
	for _, item := range arr {
		set[item] = struct{}{}
	}
	return set
}

func ArraysEqual[E comparable](arr1, arr2 []E) bool {
	set1 := arrayToSet(arr1)
	set2 := arrayToSet(arr2)

	if len(set1) != len(set2) {
		return false
	}

	for item := range set1 {
		if _, found := set2[item]; !found {
			return false
		}
	}

	return true
}

func MapsEqual[K, V comparable](map1, map2 map[K]V) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value1 := range map1 {
		value2, ok := map2[key]
		if !ok || value1 != value2 {
			return false
		}
	}
	return true
}

func ArrayDiff[E comparable](arr1, arr2 []E) []E {
	set := arrayToSet(arr2)

	var difference []E

	for _, item := range arr1 {
		if _, found := set[item]; !found {
			difference = append(difference, item)
		}
	}

	return difference
}

func MapDiff[K, V comparable](map1, map2 map[K]V) map[K]V {
	differenceMap := make(map[K]V)

	for key, val1 := range map1 {
		if val2, ok := map2[key]; !ok || val2 != val1 {
			differenceMap[key] = val1
		}
	}

	return differenceMap
}
