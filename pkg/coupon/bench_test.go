package coupon

import "testing"

func benchService(b *testing.B, idx Index) *Service {
	s := &Service{}
	s.idx.Store(&idx)
	return s
}

func BenchmarkCheckMemory(b *testing.B) {
	s := benchService(b, NewMemoryIndex([]string{"HAPPYHRS", "FIFTYOFF", "SIXTYOFF", "BIRTHDAY"}))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Check("HAPPYHRS")
			s.Check("NOTACODE1")
		}
	})
}

func BenchmarkCheckRedis(b *testing.B) {
	r := NewRedisIndex("localhost:6379", "", "coupons:valid")
	if ok, err := r.Exists(); err != nil || !ok {
		b.Skip("redis with index not reachable on localhost:6379")
	}
	s := benchService(b, r)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Check("HAPPYHRS")
		}
	})
}
