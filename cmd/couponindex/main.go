// Command couponindex is the OFFLINE job that turns the huge coupon files into
// the small exact index the API serves from. Run it from CI/cron/a one-off
// container; API replicas never read the big files.
package main

import (
	"log"

	"github.com/Santosh3204/oolio-order-food-online/pkg/config"
	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
)

func main() {
	cfg := config.CouponConfig()
	log.Printf("coupon index: store=%s sources=%v partitions=%d tmp=%s", cfg.Store, cfg.Sources, cfg.Partitions, cfg.TmpDir)
	n, err := coupon.Build(cfg)
	if err != nil {
		log.Fatalf("coupon index failed: %v", err)
	}
	log.Printf("coupon index done: %d valid codes published", n)
}
