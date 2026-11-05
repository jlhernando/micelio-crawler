package server

import (
	"log"
	"os"
	"path/filepath"

	"github.com/micelio/micelio/internal/storage"
)

// scrubbedMarker records that the crawl databases in a directory have had
// their stored configs scrubbed, so the sweep runs once per install.
const scrubbedMarker = ".configs-scrubbed-v1"

// scrubLegacyCrawlConfigs removes credentials from the config stored in
// every crawl database in dir. Versions before configs were scrubbed on
// save kept cookies, auth headers, API keys and passwords there. It runs
// once (a marker file records it) and before any crawl starts, so no other
// connection holds the files. Failures are logged and the marker is not
// written, so the next start tries again.
func scrubLegacyCrawlConfigs(dir string) {
	marker := filepath.Join(dir, scrubbedMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	failed := 0
	for _, p := range paths {
		cs, err := storage.NewCrawlStore(p)
		if err == nil {
			err = cs.ScrubConfig()
			cs.Close()
		}
		if err != nil {
			failed++
			log.Printf("[secrets] scrub stored config of %s: %v", filepath.Base(p), err)
		}
	}
	if failed > 0 {
		return
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		log.Printf("[secrets] write %s: %v", marker, err)
	}
	if len(paths) > 0 {
		log.Printf("[secrets] removed stored credentials from %d crawl databases", len(paths))
	}
}
