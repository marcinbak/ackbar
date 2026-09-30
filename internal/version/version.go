package version

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version follows date-based versioning YYYYMMDD.rev loaded from the VERSION file
var Version = strings.TrimSpace(rawVersion)

// Compare compares two version strings (e.g. "20260930.02" vs "20260930.01", or "v1.2.3" vs "1.2.4").
// Returns:
//
//	 1 if vA > vB
//	-1 if vA < vB
//	 0 if vA == vB
func Compare(vA, vB string) int {
	normA, preA := normalizeVersion(vA)
	normB, preB := normalizeVersion(vB)

	partsA := splitVersionParts(normA)
	partsB := splitVersionParts(normB)

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		var pA, pB string
		if i < len(partsA) {
			pA = partsA[i]
		}
		if i < len(partsB) {
			pB = partsB[i]
		}

		numA, errA := strconv.ParseInt(pA, 10, 64)
		numB, errB := strconv.ParseInt(pB, 10, 64)

		if errA == nil && errB == nil {
			if numA > numB {
				return 1
			}
			if numA < numB {
				return -1
			}
		} else {
			cmp := strings.Compare(pA, pB)
			if cmp != 0 {
				return cmp
			}
		}
	}

	// Base versions are equal. If one has a prerelease/snapshot suffix and the other doesn't,
	// the release (without prerelease) is considered newer.
	if preA != "" && preB == "" {
		return -1
	}
	if preA == "" && preB != "" {
		return 1
	}
	if preA != "" && preB != "" {
		return strings.Compare(preA, preB)
	}

	return 0
}

// IsNewer returns true if candidate is strictly newer than baseline.
func IsNewer(candidate, baseline string) bool {
	candidate = strings.TrimSpace(candidate)
	baseline = strings.TrimSpace(baseline)
	if candidate == "" || candidate == "unknown" {
		return false
	}
	if baseline == "" || baseline == "unknown" {
		return true
	}
	return Compare(candidate, baseline) > 0
}

func normalizeVersion(v string) (base string, prerelease string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	// Separate prerelease/build metadata (e.g. 20260930.02-snapshot)
	if idx := strings.IndexAny(v, "-+"); idx != -1 {
		base = v[:idx]
		prerelease = v[idx+1:]
	} else {
		base = v
	}
	return base, prerelease
}

func splitVersionParts(v string) []string {
	if v == "" {
		return nil
	}
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '_'
	})
}
