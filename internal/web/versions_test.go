package web_test

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVersions(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	a := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`, 201))
	admin.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "default": {"port": 2}}}`, 200)
	b := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "b.example.com", "default": {"port": 3}}}`, 201))
	admin.want("DELETE", "/api/sites/"+b, "", 204)

	var stats struct {
		Versions, Purgeable, Bytes, Sites int64
		Oldest, Newest                    time.Time
		Days                              []struct {
			Date                               string
			Versions, Purgeable                int64
			Created, Updated, Deleted, Initial int64
		}
	}
	body := admin.want("GET", "/api/versions/stats?offset=480", "", 200, `"versions":4`)
	if err := json.Unmarshal([]byte(body), &stats); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(time.FixedZone("", 8*3600)).Format(time.DateOnly)
	if stats.Purgeable != 3 || stats.Sites != 2 || stats.Bytes == 0 || stats.Oldest.IsZero() || len(stats.Days) != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if d := stats.Days[0]; d.Date != today || d.Created != 2 || d.Updated != 1 || d.Deleted != 1 || d.Purgeable != 3 {
		t.Errorf("day = %+v, want today %s", d, today)
	}
	admin.want("GET", "/api/versions/stats?offset=x", "", 400)
	admin.want("GET", "/api/versions/stats?offset=100000", "", 400)

	admin.want("POST", "/api/versions/purge", `{}`, 400, "before is required")
	admin.want("POST", "/api/versions/purge", `{"before": "yesterday"}`, 400)
	admin.want("POST", "/api/versions/purge", `{"before": "2000-01-01T00:00:00Z"}`, 200, `{"deleted":0}`)
	admin.want("POST", "/api/versions/purge", `{"before": "`+time.Now().Add(time.Hour).Format(time.RFC3339)+`"}`, 200, `{"deleted":3}`)
	admin.want("GET", "/api/versions/stats", "", 200, `"versions":1,"purgeable":0`)
}

// Only super administrators see and clear versions, or all administrators
// without super administrators in the configuration.
func TestVersionsAreForSuperAdmins(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	admin.want("POST", "/api/users", `{"username": "other", "password": "other-password", "role": "admin"}`, 201)
	admin.want("POST", "/api/users", `{"username": "platform", "password": "platform-password", "role": "platform_operator"}`, 201)
	for _, name := range []string{"other", "platform"} {
		c := as(t, h, name, name+"-password")
		c.want("GET", "/api/auth/me", "", 200, `"history":false`)
		c.want("GET", "/api/versions/stats", "", 403, `"code":"forbidden"`)
		c.want("POST", "/api/versions/purge", `{"before": "2000-01-01T00:00:00Z"}`, 403)
	}
}
