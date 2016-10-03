package main

func compare(a, b map[string]bool) bool {
	if &a == &b {
		return true

	}

	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}

	}
	return true

}
func min(a, b int) int {
	if a < b {
		return a

	}
	return b

}
