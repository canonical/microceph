package common

type Set map[string]interface{}

func (s Set) Keys() []string {
	keys := make([]string, len(s))
	count := 0

	for key := range s {
		keys[count] = key
		count++
	}

	return keys
}

func (s Set) IsIn(super Set) bool {
	flag := true

	// mark flag false if any key from subset is not present in superset.
	for key := range s {
		_, ok := super[key]
		if !ok {
			flag = false
			break // Break the loop.
		}
	}

	return flag
}
