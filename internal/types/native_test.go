package types

import "testing"

func TestNativeConfigLimitsAndCombinations(t *testing.T) {
	c := CrawlConfig{NativeChrome: true, Mode: ModeSpider, RespectRobots: true, Concurrency: 10, DelayMs: 10}
	if err := c.PrepareNativeChrome(); err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != 1 || c.DelayMs != 1000 || !c.NoAgentCheck || !c.NoSoft404Probe {
		t.Fatalf("unsafe native defaults: %+v", c)
	}
	for _, change := range []func(*CrawlConfig){
		func(c *CrawlConfig) { c.HeadOnly = true }, func(c *CrawlConfig) { c.Mode = ModeSitemap }, func(c *CrawlConfig) { c.RespectRobots = false },
		func(c *CrawlConfig) { c.Cookies = "session=secret" }, func(c *CrawlConfig) { c.CustomHeaders = map[string]string{"Authorization": "secret"} },
		func(c *CrawlConfig) { c.Device = &DeviceProfile{ID: "mobile"} }, func(c *CrawlConfig) { c.CompareDevice = &DeviceProfile{ID: "desktop"} },
		func(c *CrawlConfig) { c.PageWeight = true }, func(c *CrawlConfig) { c.CheckExternal = true }, func(c *CrawlConfig) { c.DiscoverSitemaps = true },
	} {
		invalid := c
		change(&invalid)
		if err := invalid.PrepareNativeChrome(); err == nil {
			t.Fatalf("unsupported native config accepted: %+v", invalid)
		}
	}
}
