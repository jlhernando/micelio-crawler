package logs

import (
	"log"
	"os"
	"strconv"
	"sync"
)

// Limits that keep one log analysis from using all memory, CPU or DNS. They
// are generous (three 6.5 GB plain logs with ~26M distinct URLs fit) but
// finite, so a hostile or broken upload cannot take the process down. Each
// can be changed with an environment variable (also read from
// ~/.micelio/micelio.env). They apply to the dashboard and the CLI.
type analysisLimits struct {
	// MaxURLs caps the distinct URL paths kept in memory by one analysis.
	// Hits on new URLs past the cap are counted under OtherURLsPath.
	// MICELIO_MAX_LOG_URLS.
	MaxURLs int
	// MaxDecompressedBytes caps what one .gz file may expand to.
	// MICELIO_MAX_LOG_DECOMPRESSED_GB.
	MaxDecompressedBytes int64
	// MemLimit is the soft memory limit set while an analysis runs with GC
	// off, used only when the process has none (--memlimit / GOMEMLIMIT
	// win). Without it, GC off would never collect. MICELIO_LOG_MEMLIMIT_GB.
	MemLimit int64
	// MaxVerifyIPs caps the bot IPs one verification checks with DNS.
	// MICELIO_MAX_VERIFY_IPS.
	MaxVerifyIPs int
}

const (
	defaultMaxLogURLs        = 30_000_000
	defaultMaxDecompressedGB = 64
	defaultLogMemLimitGB     = 4 // same default as `micelio ui`
	defaultMaxVerifyIPs      = 5_000

	// maxTimeKeys caps the distinct days and hour-stamps per timeline map
	// (100K hours is 11 years). Dates past it are not charted.
	maxTimeKeys = 100_000
	// maxStatusCode: any other status (garbage in the log) counts as 0.
	maxStatusCode = 999
)

// OtherURLsPath is the URL row that collects hits on new URLs once
// MICELIO_MAX_LOG_URLS distinct URLs are in memory.
const OtherURLsPath = "(other URLs)"

const gib = int64(1) << 30

var limits = sync.OnceValue(func() analysisLimits {
	return analysisLimits{
		MaxURLs:              envInt("MICELIO_MAX_LOG_URLS", defaultMaxLogURLs),
		MaxDecompressedBytes: int64(envInt("MICELIO_MAX_LOG_DECOMPRESSED_GB", defaultMaxDecompressedGB)) * gib,
		MemLimit:             int64(envInt("MICELIO_LOG_MEMLIMIT_GB", defaultLogMemLimitGB)) * gib,
		MaxVerifyIPs:         envInt("MICELIO_MAX_VERIFY_IPS", defaultMaxVerifyIPs),
	}
})

// envInt reads a positive integer from the environment, else def.
func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		log.Printf("logs: ignoring %s=%q (want a positive integer)", name, s)
		return def
	}
	return n
}
