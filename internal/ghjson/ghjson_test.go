package ghjson

import "testing"

func TestUnmarshalLabelList(t *testing.T) {
	type row struct {
		N int `json:"n"`
	}
	for _, tc := range []struct {
		name    string
		out     string
		want    int
		wantErr bool
	}{
		{"zero bytes", "", 0, false},
		{"newline", "\n", 0, false},
		{"whitespace", " \t\r\n ", 0, false},
		{"empty array", "[]", 0, false},
		{"empty array and newline", "[]\n", 0, false},
		{"one row", `[{"n":1}]`, 1, false},
		{"malformed", "<html>rate limited</html>", 0, true},
		{"truncated", `[{"n":1}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []row
			err := UnmarshalLabelList([]byte(tc.out), &rows)
			if (err != nil) != tc.wantErr || len(rows) != tc.want {
				t.Errorf("UnmarshalLabelList(%q) = %d rows, err %v", tc.out, len(rows), err)
			}
		})
	}
}
