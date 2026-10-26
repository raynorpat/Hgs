// Package version compares numeric major.minor.patch dependency versions.
package version

import (
	"strconv"
	"strings"
)

func AtLeast(current, minimum string) bool {
	parse := func(s string) ([3]int, bool) {
		var v [3]int
		parts := strings.Split(s, ".")
		if len(parts) < 3 {
			return v, false
		}
		for i := range v {
			n, err := strconv.Atoi(parts[i])
			if err != nil || n < 0 {
				return v, false
			}
			v[i] = n
		}
		return v, true
	}
	a, ok := parse(current)
	b, valid := parse(minimum)
	if !ok || !valid {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}
