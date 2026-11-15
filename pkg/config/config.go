package config

import "os"

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	ServerAddress = getenv("SERVER_ADDRESS", "0.0.0.0")
	ServerPort    = getenv("SERVER_PORT", "8080")
	// APIKey is the key expected in the `api_key` header of secured endpoints.
	APIKey = getenv("API_KEY", "apitest")
	// CouponFiles is a comma-separated list of paths/URLs (.gz supported) of coupon files.
	CouponFiles = getenv("COUPON_FILES", "https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase1.gz,https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase2.gz,https://orderfoodonline-files.s3.ap-southeast-2.amazonaws.com/couponbase3.gz")
)
