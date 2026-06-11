package mcp

import "testing"

func TestGuardReadOnlySQL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		ok   bool
	}{
		{"plain select", "SELECT * FROM pages", true},
		{"lowercase", "select url from pages", true},
		{"with cte", "WITH x AS (SELECT 1) SELECT * FROM x", true},
		{"explain", "EXPLAIN QUERY PLAN SELECT 1", true},
		{"values", "VALUES (1),(2)", true},
		{"leading line comment", "-- hi\nSELECT 1", true},
		{"leading block comment", "/* c */ SELECT 1", true},
		{"trailing semicolon ok", "SELECT 1;", true},
		{"pragma table_info", "PRAGMA table_info(pages)", true},

		{"attach blocked", "ATTACH DATABASE '/etc/passwd' AS p", false},
		{"detach blocked", "DETACH DATABASE p", false},
		{"insert blocked", "INSERT INTO pages VALUES (1)", false},
		{"update blocked", "UPDATE pages SET url=''", false},
		{"delete blocked", "DELETE FROM pages", false},
		{"drop blocked", "DROP TABLE pages", false},
		{"multi statement select then attach", "SELECT 1; ATTACH DATABASE '/etc/passwd' AS p", false},
		{"multi statement trailing", "SELECT 1; SELECT 2", false},
		{"empty after trim", "   ", false},
		{"comment hiding attach is still single-keyword attach", "/* SELECT */ ATTACH DATABASE 'x' AS p", false},
	}
	for _, c := range cases {
		err := guardReadOnlySQL(c.sql)
		if (err == nil) != c.ok {
			t.Errorf("%s: guardReadOnlySQL(%q) err=%v, want ok=%v", c.name, c.sql, err, c.ok)
		}
	}
}
