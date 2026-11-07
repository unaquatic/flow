package engine

import (
	"bytes"
	"sync"
	"testing"
)

// codecs returns every Codec by name.
func codecs() map[string]Codec {
	return map[string]Codec{"stored": Stored{}, "deflate": Deflate{}}
}

func TestCodec_RoundTrips(t *testing.T) {
	inputs := map[string][]byte{
		"empty":        {},
		"text":         []byte("hello\n"),
		"compressible": bytes.Repeat([]byte("func main() {}\n"), 1<<12),
		"random":       randomBytes(1 << 16),
	}
	for codecName, c := range codecs() {
		for inputName, data := range inputs {
			t.Run(codecName+"/"+inputName, func(t *testing.T) {
				const prefix = "prefix"
				dst := bytes.NewBufferString(prefix)
				if err := c.Compress(dst, data); err != nil {
					t.Fatalf("compress: %v", err)
				}
				z, ok := bytes.CutPrefix(dst.Bytes(), []byte(prefix))
				if !ok {
					t.Fatal("Compress overwrote what dst held instead of appending")
				}
				got, err := c.Decompress(z, int64(len(data)))
				if err != nil {
					t.Fatalf("decompress: %v", err)
				}
				if !bytes.Equal(got, data) {
					t.Errorf("round trip turned %d bytes into %d that differ", len(data), len(got))
				}
			})
		}
	}
}

func TestCodec_RejectsWrongSize(t *testing.T) {
	data := []byte("twelve bytes")
	for name, c := range codecs() {
		t.Run(name, func(t *testing.T) {
			var z bytes.Buffer
			if err := c.Compress(&z, data); err != nil {
				t.Fatal(err)
			}
			for _, size := range []int64{0, int64(len(data)) - 1, int64(len(data)) + 1} {
				if _, err := c.Decompress(z.Bytes(), size); err == nil {
					t.Errorf("Decompress to %d bytes of %d-byte content succeeded", size, len(data))
				}
			}
		})
	}
}

func TestDeflate_RejectsCorruptData(t *testing.T) {
	if _, err := (Deflate{}).Decompress([]byte("not deflate data"), 5); err == nil {
		t.Error("Decompress of corrupt data succeeded")
	}
}

// TestDeflate_IsSafeForConcurrentUse pins the Codec contract the pipeline
// relies on. Run it with -race.
func TestDeflate_IsSafeForConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			data := bytes.Repeat([]byte{byte('a' + i)}, 1<<14+i)
			for range 20 {
				var z bytes.Buffer
				if err := (Deflate{}).Compress(&z, data); err != nil {
					t.Errorf("compress: %v", err)
					return
				}
				got, err := (Deflate{}).Decompress(z.Bytes(), int64(len(data)))
				if err != nil || !bytes.Equal(got, data) {
					t.Errorf("worker %d read back %d bytes, %v; want its own %d", i, len(got), err, len(data))
					return
				}
			}
		})
	}
	wg.Wait()
}
