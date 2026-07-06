package redact

import "strings"

const Replacement = "[REDACTED]"

type Redactor struct {
	values []string
}

func New(values ...string) Redactor {
	seen := map[string]struct{}{}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		filtered = append(filtered, value)
	}
	return Redactor{values: filtered}
}

func (r Redactor) With(values ...string) Redactor {
	all := append([]string{}, r.values...)
	all = append(all, values...)
	return New(all...)
}

func (r Redactor) String(s string) string {
	out := s
	for _, value := range r.values {
		out = strings.ReplaceAll(out, value, Replacement)
	}
	return out
}

func (r Redactor) Any(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case string:
		return r.String(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = r.String(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = r.Any(item)
		}
		return out
	case []map[string]string:
		out := make([]map[string]string, len(typed))
		for i, item := range typed {
			out[i] = r.Any(item).(map[string]string)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for i, item := range typed {
			out[i] = r.Any(item).(map[string]any)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for k, value := range typed {
			out[r.String(k)] = r.String(value)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, value := range typed {
			out[r.String(k)] = r.Any(value)
		}
		return out
	default:
		return v
	}
}
