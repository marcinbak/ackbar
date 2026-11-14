package mcp

import (
	"strings"
)

func isValidUUID(u string) bool {
	if len(u) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		b := u[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if b != '-' {
				return false
			}
		} else {
			if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
				return false
			}
		}
	}
	return true
}

func resolveSessionFromList(sessions []map[string]any, candidate string) map[string]any {
	if candidate == "" || len(sessions) == 0 {
		return nil
	}

	for _, s := range sessions {
		if strings.EqualFold(getString(s, "id"), candidate) || strings.EqualFold(getString(s, "old_id"), candidate) {
			return s
		}
	}

	for _, s := range sessions {
		if strings.EqualFold(getString(s, "native_id"), candidate) {
			return s
		}
	}

	candUUID := ""
	if isValidUUID(candidate) {
		candUUID = strings.ToLower(candidate)
	} else if idx := strings.LastIndex(candidate, ":"); idx != -1 && idx+1 < len(candidate) {
		sub := candidate[idx+1:]
		if isValidUUID(sub) {
			candUUID = strings.ToLower(sub)
		}
	} else if len(candidate) > 37 && (candidate[len(candidate)-37] == '-' || candidate[len(candidate)-37] == ':') {
		sub := candidate[len(candidate)-36:]
		if isValidUUID(sub) {
			candUUID = strings.ToLower(sub)
		}
	}

	if candUUID != "" {
		for _, s := range sessions {
			nativeID := strings.ToLower(getString(s, "native_id"))
			id := strings.ToLower(getString(s, "id"))
			oldID := strings.ToLower(getString(s, "old_id"))
			if nativeID == candUUID || strings.HasSuffix(id, ":"+candUUID) || strings.HasSuffix(oldID, ":"+candUUID) {
				return s
			}
		}
	}

	return nil
}

func matchesSessionAlias(s map[string]any, candidate string) bool {
	return resolveSessionFromList([]map[string]any{s}, candidate) != nil
}
