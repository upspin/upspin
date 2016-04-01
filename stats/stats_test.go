package stats

import (
	"io"
	"testing"

	"upspin.googlesource.com/upspin.git/cloud/netutil/nettest"
)

func TestRegistrationAndPrint(t *testing.T) {
	w := nettest.NewExpectingResponseWriter("=== mocking ===\nThis is a test\n---\n")
	m := &MockStat{
		t:              t,
		messageToWrite: "This is a test",
	}
	err := New("mocking", m)
	if err != nil {
		t.Fatal(err)
	}
	err = New("mocking", nil)
	if err != ErrExists {
		t.Errorf("Expected error %s, got %s", ErrExists, err)
	}
	mRet := Lookup("mocking")
	if mRet == nil {
		t.Fatal("Expected non nil")
	}
	if mRet.(*MockStat) != m {
		t.Fatal("Expected same object, but got a different one")
	}
	OutputReport(nil, w)
	w.Verify(t)
}

type MockStat struct {
	messageToWrite string
	message        string
	t              *testing.T
}

var _ Stats = (*MockStat)(nil)

func (m *MockStat) Update() {
	m.message = m.messageToWrite
}

func (m *MockStat) Print(w io.Writer) {
	_, err := w.Write([]byte(m.message))
	if err != nil {
		m.t.Fatal(err)
	}
}
