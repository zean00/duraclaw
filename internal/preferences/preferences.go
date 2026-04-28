package preferences

import (
	"encoding/json"
	"fmt"
	"strings"

	"duraclaw/internal/db"
)

func Match(list []db.Preference, context map[string]any) []db.Preference {
	var out []db.Preference
	for _, pref := range list {
		if ConditionMatches(pref.Condition, context) {
			out = append(out, pref)
		}
	}
	return out
}

func ConditionMatches(raw json.RawMessage, context map[string]any) bool {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return true
	}
	var condition map[string]any
	if err := json.Unmarshal(raw, &condition); err != nil {
		return false
	}
	for key, expected := range condition {
		actual, ok := context[key]
		if !ok {
			return false
		}
		if !matches(actual, expected) {
			return false
		}
	}
	return true
}

func matches(actual, expected any) bool {
	if values, ok := expected.([]any); ok {
		for _, value := range values {
			if matches(actual, value) {
				return true
			}
		}
		return false
	}
	return strings.EqualFold(fmt.Sprint(actual), fmt.Sprint(expected))
}
