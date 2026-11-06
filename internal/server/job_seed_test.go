package server

import (
	"testing"

	"zombiezen.com/go/sqlite/sqlitex"
)

func TestJobSeedURL(t *testing.T) {
	cases := []struct {
		cfg  map[string]interface{}
		want string
	}{
		{map[string]interface{}{"seedUrl": "https://a.test/"}, "https://a.test/"},
		{map[string]interface{}{"url": "https://b.test/"}, "https://b.test/"},
		{map[string]interface{}{"seedUrl": "", "mode": "list", "urlList": "\n  https://c.test/x\nhttps://c.test/y"}, "https://c.test/x"},
		{map[string]interface{}{"mode": "list", "urls": []interface{}{"", "https://d.test/1"}}, "https://d.test/1"},
		{map[string]interface{}{"mode": "sitemap", "sitemapUrls": []interface{}{"https://e.test/sitemap.xml"}}, "https://e.test/sitemap.xml"},
		{map[string]interface{}{"mode": "list"}, ""},
	}
	for _, c := range cases {
		if got := jobSeedURL(c.cfg); got != c.want {
			t.Errorf("jobSeedURL(%v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

// List crawls are shown under their first URL, also jobs saved before the
// fallback existed (empty seed_url in the table).
func TestListCrawlJobHasSeedURL(t *testing.T) {
	s := newTestStore(t)
	job, err := s.CreateCrawlJob(map[string]interface{}{"mode": "list", "urlList": "https://shop.test/a\nhttps://shop.test/b"})
	if err != nil {
		t.Fatal(err)
	}
	if job.SeedURL != "https://shop.test/a" {
		t.Errorf("created job SeedURL = %q", job.SeedURL)
	}
	// An older row with no seed_url.
	s.mu.Lock()
	err = sqlitex.Execute(s.conn, `UPDATE crawl_jobs SET seed_url = '' WHERE id = ?`, &sqlitex.ExecOptions{Args: []interface{}{job.ID}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCrawlJob(job.ID)
	if got == nil || got.SeedURL != "https://shop.test/a" {
		t.Errorf("legacy job SeedURL = %v", got)
	}
	list, _ := s.ListCrawlJobs()
	if len(list) != 1 || list[0].SeedURL != "https://shop.test/a" {
		t.Errorf("listed SeedURL = %+v", list)
	}
}
