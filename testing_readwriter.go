package at

import (
	"io"
)

var _ io.ReadWriter = &TestReadWriter{}

// TestReadWriter is a readwriter implementation used for testing.
// Responses to Read and Write calls are configured by Reads (ReadResult) and and Writes (WriteResults)
type TestReadWriter struct {
	Reads     chan ReadResult
	Writes    chan WriteResult
	LastInput []byte
}

func NewTestReadWriter() *TestReadWriter {
	return &TestReadWriter{
		Reads:  make(chan ReadResult, 1000),
		Writes: make(chan WriteResult, 1000),
	}
}

func (t *TestReadWriter) Read(buf []byte) (int, error) {
	if t.Reads == nil {
		panic("no read chan created")
	}
	r := <-t.Reads

	var n int
	if (r.OutData) != nil {
		n = copy(buf, r.OutData)
	}

	if r.N != nil {
		n = *r.N
	}

	return n, r.Err
}

func (t *TestReadWriter) Write(buf []byte) (int, error) {
	t.LastInput = append(t.LastInput[0:], buf...)

	if t.Writes == nil {
		panic("no write chan to created")
	}
	var w WriteResult
	select {
	case w = <-t.Writes:
	default:
		// default write is:
		w = WriteResult{}
	}

	var n int
	n = len(buf)

	if w.N != nil {
		n = *w.N
	}

	return n, w.Err
}

// ReadResult represents the return values from a Read operation
type ReadResult struct {
	// if nil the actual n is used
	N       *int
	Err     error
	OutData []byte
}

// WriteResult represents the return values from a Write operation
type WriteResult struct {
	// if nil the actual n is used
	N   *int
	Err error
}

// WithReadResult adds the desired read result and returns a new TestReadWriter
func (t *TestReadWriter) WithReadResult(outData []byte, err error, n ...int) *TestReadWriter {
	if len(n) > 1 {
		panic("only 0 or 1 int's allowed in WithReadResult")
	}
	var nPtr *int
	if len(n) == 1 {
		nPtr = &n[0]
	}

	t.Reads <- ReadResult{
		Err:     err,
		N:       nPtr,
		OutData: outData,
	}

	return t
}

// WithWriteResult adds the desired write result and returns a new TestReadWriter
func (t *TestReadWriter) WithWriteResult(err error, n ...int) *TestReadWriter {
	if len(n) > 1 {
		panic("only 0 or 1 int's allowed in WithWriteResult")
	}
	var nPtr *int
	if len(n) == 1 {
		nPtr = &n[0]
	}

	t.Writes <- WriteResult{
		Err: err,
		N:   nPtr,
	}

	return t
}
