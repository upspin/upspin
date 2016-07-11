package main

import (
	"io"
	"strings"
	"testing"

	"sync"

	"math/rand"

	"time"

	"upspin.io/errors"
)

const file = "a@b.com/somefile"

func TestSmallBlocks(t *testing.T) {
	f := NewBufferedChannelFile(file, 3)

	n, err := f.Write([]byte("1"))
	expect(t, n, err, 1, nil)

	n, err = f.Write([]byte("23")) // Should fill buffer
	expect(t, n, err, 2, nil)
	f.Close()

	buf := make([]byte, 3)
	n, err = f.Read(buf)
	expect(t, n, err, 3, nil)
	if string(buf) != "123" {
		t.Fatalf("Expected 123 got %q", buf)
	}

	// No more data.
	n, err = f.Read(buf)
	expect(t, n, err, 0, io.EOF)
	n, err = f.Read(buf)
	expect(t, n, err, 0, io.EOF)

	// Can't write once file is closed.
	n, err = f.Write([]byte("meh"))
	expect(t, n, err, 0, errors.Str("closed"))
}

func TestBigBlocks(t *testing.T) {
	f := NewBufferedChannelFile(file, 1024)

	str := "foo bar"
	n, err := f.Write([]byte(str))
	expect(t, n, err, len(str), nil)

	str = " - "
	n, err = f.Write([]byte(str))
	expect(t, n, err, len(str), nil)

	str = "last block"
	n, err = f.Write([]byte(str))
	expect(t, n, err, len(str), nil)

	f.Close()

	var buf [2000]byte
	n, err = f.Read(buf[:])
	expected := "foo bar - last block"
	expect(t, n, err, len(expected), io.EOF)
	if string(buf[:n]) != expected {
		t.Fatalf("Expected %q, got %q", expected, buf[:n])
	}
}

func TestBigWrite(t *testing.T) {
	const data = "some very long string; much longer than the block size"
	f := NewBufferedChannelFile(file, 5)

	// This would block, so put in a go routine
	go func() {
		n, err := f.Write([]byte(data))
		expect(t, n, err, len(data), nil)
		f.Close()
	}()

	var buf [2000]byte
	n, err := f.Read(buf[:])
	expected := data
	expect(t, n, err, len(expected), io.EOF)
	if string(buf[:n]) != expected {
		t.Fatalf("Expected %q, got %q", expected, buf[:n])
	}
}

func TestBigRead(t *testing.T) {
	const data = "another string which is again bigger than the block size"
	f := NewBufferedChannelFile(file, 5)

	ch := make(chan bool)

	// This would block, so put in a go routine
	go func() {
		outerBuf := make([]byte, 2000)
		var total int
		ch <- true
		for {
			var buf [3]byte
			n, err := f.Read(buf[:])
			if n > 0 {
				copy(outerBuf[total:], buf[:n])
				total += n
			}
			if err == io.EOF {
				break
			}
		}
		expected := data
		if string(outerBuf[:total]) != expected {
			t.Fatalf("Expected %q, got %q", expected, outerBuf[:total])
		}
		ch <- true
	}()

	<-ch
	n, err := f.Write([]byte(data))
	expect(t, n, err, len(data), nil)
	f.Close()
	<-ch
}

func testParallel(t *testing.T, bufferSize int) {
	// Create a data set with each byte equal to its offset.
	data := make([]byte, 10240)
	for i := range data {
		data[i] = uint8(i)
	}

	f := NewBufferedChannelFile(file, bufferSize)

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		var offset int

		for {
			if offset == len(data) {
				break
			}

			// Write buffer is of variable size: [1-50] bytes
			bufSize := int(rand.Int31n(50) + 1)
			buf := make([]byte, bufSize)
			remaining := len(data) - offset
			var copied int
			if bufSize < remaining {
				copied = copy(buf[:], data[offset:offset+bufSize])
			} else {
				copied = copy(buf[:], data[offset:offset+remaining])
				buf = buf[:copied]
			}
			n, err := f.Write(buf)
			if err != nil {
				t.Fatal(err)
			}
			if n != copied {
				t.Fatalf("Expected write %d, wrote %d", copied, n)
			}
			offset += n
			// Sleep for up to 10 ms
			time.Sleep(time.Duration(rand.Int31n(10)) * time.Millisecond)
		}
		f.Close()
		wg.Done()
	}()

	go func() {
		runningBuffer := make([]byte, len(data)+5)
		var offset int

		for {
			// Read buffer is of variable size: [1-30] bytes.
			bufSize := rand.Int31n(30) + 1
			buf := make([]byte, bufSize)
			n, err := f.Read(buf)
			if n > 0 {
				copy(runningBuffer[offset:], buf[:n])
				offset += n
			}
			if err != nil {
				if err == io.EOF {
					break
				}
				t.Fatal(err)
			}
			// Sleep for up to 10 ms
			time.Sleep(time.Duration(rand.Int31n(10)) * time.Millisecond)
		}
		runningBuffer = runningBuffer[:offset]
		if len(runningBuffer) != len(data) {
			t.Fatalf("Expected %d bytes, got %d", len(data), len(runningBuffer))
		}
		// Compare runningBuffer to data.
		for i := range runningBuffer {
			if uint8(data[i]) != uint8(runningBuffer[i]) {
				t.Errorf("Byte %d mismatched", i)
			}
		}
		wg.Done()
	}()

	wg.Wait()
}

func TestParallel10(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	testParallel(t, 10)
}

func TestParallel1(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	testParallel(t, 1)
}

func TestParallel17(t *testing.T) {
	testParallel(t, 17)
}

func TestParallel1024(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	testParallel(t, 1024)
}

func expect(t *testing.T, n int, err error, nExpected int, errExpected error) {
	if err != nil {
		if errExpected == nil {
			t.Fatalf("Expected no error, got %s", err)
		}
		if !strings.Contains(err.Error(), errExpected.Error()) {
			t.Fatalf("Expected error contains %q, got %q", errExpected, err)
		}

	}
	if n != nExpected {
		t.Errorf("Expected %d bytes, got %d", nExpected, n)
	}
}
