package config

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

func getenvIntAllowZero(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n >= 0 {
		return n
	}
	return def
}

const defaultCouponFiles = "https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase1.gz," +
	"https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase2.gz," +
	"https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase3.gz"

var (
	ServerAddress = getenv("SERVER_ADDRESS", "0.0.0.0")
	ServerPort    = getenv("SERVER_PORT", "8080")
	// APIKey is the key expected in the `api_key` header of secured endpoints.
	APIKey = getenv("API_KEY", "apitest")

	// Per-client limit on POST /order (also throttles coupon-code guessing).
	RateLimitPerMin = getenvInt("RATE_LIMIT_PER_MIN", 60)
	// Only set true behind a trusted reverse proxy that appends to X-Forwarded-For
	// (nginx in docker-compose does); otherwise clients could spoof their identity.
	RateLimitTrustProxy = getenv("RATE_LIMIT_TRUST_PROXY", "false") == "true"
)

// CouponConfig reads the coupon settings from the environment.
//
//	COUPON_STORE        file | redis                       (default file)
//	COUPON_INDEX        file store: index path or URL      (default data/coupon-index.txt)
//	COUPON_REDIS_ADDR   redis store address                (default localhost:6379)
//	COUPON_REDIS_KEY    redis SET key                      (default coupons:valid)
//	COUPON_FILES        comma-separated source files/URLs  (default: the 3 challenge S3 files)
//	COUPON_TMP_DIR      spill dir for the offline build    (default data/tmp)
//	COUPON_PARTITIONS   build partitions                   (default 64)
//	COUPON_BUILD_WORKERS partitions reduced in parallel    (default min(4, CPUs))
//	COUPON_REFRESH_SECONDS reload the index this often     (default 60, 0 = never)
//	COUPON_AUTOBUILD    API builds a missing index itself  (default true; set false in prod)
//	COUPON_SOURCE_CONCURRENCY sources streamed at once     (default 4)
//	COUPON_SPILL_MEMORY_MB spill buffer budget             (default 256)
//	COUPON_RESUME       resume a failed build              (default true)
//	COUPON_COOPERATIVE  several processes share the build  (default false; needs shared COUPON_TMP_DIR)
//	COUPON_BUILD_ID     cooperative: id of this run        (change per scheduled run)
func CouponConfig() coupon.Config {
	var sources []string
	for _, s := range strings.Split(getenv("COUPON_FILES", defaultCouponFiles), ",") {
		if s = strings.TrimSpace(s); s != "" {
			sources = append(sources, s)
		}
	}
	return coupon.Config{
		Store:         getenv("COUPON_STORE", "file"),
		IndexPath:     getenv("COUPON_INDEX", "data/coupon-index.txt"),
		RedisAddr:     getenv("COUPON_REDIS_ADDR", "localhost:6379"),
		RedisPassword: os.Getenv("COUPON_REDIS_PASSWORD"),
		RedisKey:      getenv("COUPON_REDIS_KEY", "coupons:valid"),
		Sources:       sources,
		TmpDir:        getenv("COUPON_TMP_DIR", "data/tmp"),
		Partitions:    getenvInt("COUPON_PARTITIONS", 64),
		Workers:       getenvInt("COUPON_BUILD_WORKERS", min(4, runtime.NumCPU())),
		Refresh:       time.Duration(getenvIntAllowZero("COUPON_REFRESH_SECONDS", 60)) * time.Second,
		AutoBuild:     getenv("COUPON_AUTOBUILD", "true") != "false",

		SourceConcurrency: getenvInt("COUPON_SOURCE_CONCURRENCY", 4),
		SpillMemoryMB:     getenvInt("COUPON_SPILL_MEMORY_MB", 256),
		Resume:            getenv("COUPON_RESUME", "true") != "false",
		Cooperative:       getenv("COUPON_COOPERATIVE", "false") == "true",
		BuildID:           os.Getenv("COUPON_BUILD_ID"),
	}
}
