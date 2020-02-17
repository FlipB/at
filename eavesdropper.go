package at

import (
	"bytes"
	"io"
	"log"
)

/*
	Eavesdropping readwriter for debugging purposes
*/

var _ io.ReadWriter = eavesdrop{}

type eavesdrop struct {
	io.ReadWriter
}

func (e eavesdrop) Read(b []byte) (int, error) {
	n, err := e.ReadWriter.Read(b)
	nb := bytes.ReplaceAll(bytes.ReplaceAll(b[0:n], []byte("\r"), []byte("<CR>")), []byte("\n"), []byte("<LF>"))
	log.Printf("Eaves: Read %d bytes, Err = %v, Data = %s\n", n, err, nb)
	return n, err
}

func (e eavesdrop) Write(b []byte) (int, error) {
	nb := []byte(string(b))
	n, err := e.ReadWriter.Write(b)
	nb = bytes.ReplaceAll(bytes.ReplaceAll(b[0:n], []byte("\r"), []byte("<CR>")), []byte("\n"), []byte("<LF>"))
	log.Printf("Eaves: Wrote %d bytes, Err = %v, Data = %s\n", n, err, nb)
	return 0, nil
}
