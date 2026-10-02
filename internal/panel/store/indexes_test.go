package store

import (
	"context"
	"strings"
	"testing"
)

// The tables keyed by (user_id, time) are read and pruned by time alone: the primary key
// cannot serve that, the time indexes of migration 0017 do. A plan that scans the table
// (and a year of 10 000 users is millions of rows) fails here.
func TestTimeQueriesUseTheirIndex(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, c := range []struct{ sql, table, index string }{
		{"DELETE FROM traffic_hourly WHERE hour < 1", "traffic_hourly", "traffic_hourly_hour"},
		{"DELETE FROM traffic_daily WHERE day < 1", "traffic_daily", "traffic_daily_day"},
		{"SELECT hour, sum(up), sum(down) FROM traffic_hourly WHERE hour >= 1 GROUP BY hour ORDER BY hour", "traffic_hourly", "traffic_hourly_hour"},
		{"SELECT day, sum(up), sum(down) FROM traffic_daily WHERE day >= 1 GROUP BY day ORDER BY day", "traffic_daily", "traffic_daily_day"},
		{"SELECT u.id, sum(d.up + d.down) AS b FROM traffic_daily d JOIN users u ON u.id = d.user_id WHERE d.day >= 1 GROUP BY u.id ORDER BY b DESC LIMIT 5", "traffic_daily", "traffic_daily_day"},
		{"DELETE FROM devices WHERE last_seen < 1", "devices", "devices_last_seen"},
		{"DELETE FROM audit_log WHERE ts < 1", "audit_log", "audit_log_ts"},
	} {
		rows, err := st.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+c.sql)
		if err != nil {
			t.Fatalf("%s: %v", c.sql, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		text := strings.Join(plan, "; ")
		if !strings.Contains(text, c.index) {
			t.Errorf("%s\n  plan: %s\n  want a search by %s", c.sql, text, c.index)
		}
		if strings.Contains(text, "SCAN "+c.table) && !strings.Contains(text, c.index) {
			t.Errorf("%s scans %s: %s", c.sql, c.table, text)
		}
	}
}
