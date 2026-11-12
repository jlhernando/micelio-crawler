package server

// applyDownloadImagesDefault fills a crawl config that does not say whether
// to download images (API callers, MCP, schedules saved before the setting
// existed) from Settings → Images. Without the setting the crawl config's
// zero value applies: images are references only, nothing is downloaded.
func applyDownloadImagesDefault(store *UiStore, config map[string]interface{}) {
	if _, ok := config["downloadImages"]; ok || store == nil {
		return
	}
	settings, err := store.GetSettings()
	if err != nil {
		return
	}
	if v, ok := settings["downloadImages"].(bool); ok && v {
		config["downloadImages"] = true
	}
}
