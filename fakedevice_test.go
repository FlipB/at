package at

import (
	"fmt"
	"io"
	"sync"
)

var _ io.ReadWriter = &FakeDeviceReadWriter{}

// FakeDeviceReadWriter is a readwriter implementation used for testing.
// Responses to Read and Write calls are configured by Reads (ReadResult) and and Writes (WriteResults)
type FakeDeviceReadWriter struct {
	ReadReqs  chan ReadRequest
	WriteReqs chan WriteRequest
	closer    sync.Once
	ended     chan struct{}
}

func NewFakeDeviceReadWriter(deviceRoutine func(<-chan WriteRequest, <-chan ReadRequest) error) (*FakeDeviceReadWriter, <-chan error) {
	rw := &FakeDeviceReadWriter{
		ReadReqs:  make(chan ReadRequest, 0),
		WriteReqs: make(chan WriteRequest, 0),
		ended:     make(chan struct{}, 0),
	}
	testErrChan := make(chan error, 1)
	go func() {
		defer close(testErrChan)
		defer rw.Close()
		err := deviceRoutine(rw.WriteReqs, rw.ReadReqs)
		if err != nil {
			testErrChan <- err
			return
		}
		// Test should be done - check to ensure we dont get additional inputs
		select {
		case data, ok := <-rw.WriteReqs:
			if ok {
				close(data.Response)
				testErrChan <- fmt.Errorf("unexpected additional write (data = %s)", data.InData)
			}
		case data, ok := <-rw.ReadReqs:
			if ok {
				close(data.Response)
				testErrChan <- fmt.Errorf("unexpected additional read")
			}
		}

	}()
	return rw, testErrChan
}

func (t *FakeDeviceReadWriter) Close() {
	t.closer.Do(func() {
		close(t.ended)
		close(t.ReadReqs)
		close(t.WriteReqs)
	})
}

func (t *FakeDeviceReadWriter) testEnded() bool {
	select {
	case <-t.ended:
		return true
	default:
		return false
	}
}

var ErrTestEnded = fmt.Errorf("test ended")

func (t *FakeDeviceReadWriter) Read(buf []byte) (int, error) {
	if t.ReadReqs == nil {
		panic("no read chan created")
	}
	if t.testEnded() {
		panic(ErrTestEnded)
	}
	c := make(chan ReadResponse, 0)
	select {
	case <-t.ended:
		panic(ErrTestEnded)
	case t.ReadReqs <- ReadRequest{c}:
	}

	var r ReadResponse
	select {
	case <-t.ended:
		panic(ErrTestEnded)
	case r = <-c:
	}

	var n int
	if (r.OutData) != nil {
		n = copy(buf, r.OutData)
	}

	if r.N != nil {
		n = *r.N
	}

	return n, r.Err
}

func (t *FakeDeviceReadWriter) Write(buf []byte) (int, error) {

	if t.WriteReqs == nil {
		panic("no write chan created")
	}
	if t.testEnded() {
		panic(ErrTestEnded)
	}
	c := make(chan WriteResponse, 0)
	select {
	case <-t.ended:
		panic(ErrTestEnded)
	case t.WriteReqs <- WriteRequest{
		InData:   buf,
		Response: c,
	}:
	}

	var w WriteResponse
	select {
	case <-t.ended:
		panic(ErrTestEnded)
	case w = <-c:
	}

	var n int
	n = len(buf)

	if w.N != nil {
		n = *w.N
	}

	return n, w.Err
}

// ReadRequest represents the return values from a Read operation
type ReadRequest struct {
	Response chan ReadResponse
}

// ReadResponse represents the return values from a Read operation
type ReadResponse struct {
	// if nil the actual n is used
	N       *int
	Err     error
	OutData []byte
}

// WriteRequest represents the return values from a Write operation
type WriteRequest struct {
	InData   []byte
	Response chan WriteResponse
}

// WriteResponse represents the return values from a Write operation
type WriteResponse struct {
	// if nil the actual n is used
	N   *int
	Err error
}
