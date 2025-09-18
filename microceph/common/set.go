package common

type Set map[string]any

// Keys returns a slice of keys in the set.
func (s Set) Keys() []string {
	keys := make([]string, len(s))
	count := 0

	for key := range s {
		keys[count] = key
		count++
	}

	return keys
}

// Insert puts a key in the set.
func (s Set) Insert(key string) {
	_, ok := s[key]
	if !ok {
		s[key] = nil
	}
}

// Add inserts multiple keys in the set.
func (s Set) Add(keys []string) {
	for _, key := range keys {
		s.Insert(key)
	}
}

// IsIn checks if the set is a subset of the provided super set.
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
