package coupon_test

import (
	"testing"

	"github.com/Santosh3204/oolio-order-food-online/pkg/coupon"
)

func benchService(idx coupon.Index) *coupon.Service {
	s := &coupon.Service{}
	s.SetIndex(idx)
	return s
}

func BenchmarkCheckMemory(b *testing.B) {
	s := benchService(coupon.NewMemoryIndex([]string{"HAPPYHRS", "FIFTYOFF", "SIXTYOFF", "BIRTHDAY"}))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := s.Check("HAPPYHRS")
			if err != nil {
				return
			}
			_, err = s.Check("NOTACODE1")
			if err != nil {
				return
			}
		}
	})
}

func BenchmarkCheckRedis(b *testing.B) {
	r := coupon.NewRedisIndex("localhost:6379", "", "coupons:valid")
	if ok, err := r.Exists(); err != nil || !ok {
		b.Skip("redis with index not reachable on localhost:6379")
	}
	s := benchService(r)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := s.Check("HAPPYHRS")
			if err != nil {
				return
			}
		}
	})
}
