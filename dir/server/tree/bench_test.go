package tree

import (
	"crypto/sha256"
	"testing"
)

func benchmarkChecksum(b *testing.B, size int) {
	buf := make([]byte, size)
	for i := 0; i < size; i++ {
		buf[i] = byte(i % 256)
	}
	for i := 0; i < b.N; i++ {
		checksum(buf)
	}
}

func benchmarkSHA256(b *testing.B, size int) {
	buf := make([]byte, size)
	for i := 0; i < size; i++ {
		buf[i] = byte(i % 256)
	}
	for i := 0; i < b.N; i++ {
		sha256.Sum256(buf)
	}

}

func BenchmarkChecksum_64(b *testing.B)  { benchmarkChecksum(b, 64) }
func BenchmarkChecksum_640(b *testing.B) { benchmarkChecksum(b, 640) }
func BenchmarkChecksum_1k(b *testing.B)  { benchmarkChecksum(b, 1024) }
func BenchmarkChecksum_4k(b *testing.B)  { benchmarkChecksum(b, 4096) }
func BenchmarkChecksum_1M(b *testing.B)  { benchmarkChecksum(b, 1024*1024) }
func BenchmarkChecksum_10M(b *testing.B) { benchmarkChecksum(b, 10*1024*1024) }

func BenchmarkSHA_64(b *testing.B)  { benchmarkSHA256(b, 64) }
func BenchmarkSHA_640(b *testing.B) { benchmarkSHA256(b, 640) }
func BenchmarkSHA_1k(b *testing.B)  { benchmarkSHA256(b, 1024) }
func BenchmarkSHA_4k(b *testing.B)  { benchmarkSHA256(b, 4096) }
func BenchmarkSHA_1M(b *testing.B)  { benchmarkSHA256(b, 1024*1024) }
func BenchmarkSHA_10M(b *testing.B) { benchmarkSHA256(b, 10*1024*1024) }
