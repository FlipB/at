package at

import (
	"bytes"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// cancelRead attempts to unblock pending reads by sending a known unsupported command (thus triggering "COMMAND NOT SUPPORT" output)
// writes to underlying writer WITHOUT ACQUIRING write lock
func (d *ScanWriter) cancelRead(id ...uint) error {
	if len(id) > 1 {
		panic("cancelRead only takes 0 or 1 params")
	}

	s := d.State()
	if s == ATPrompt {
		_, err := d.writer.Write([]byte{0x1b}) // send escape (cancels prompt)
		if err != nil {
			return err
		}
	}
	unblockIDString := "0"
	if len(id) == 1 {
		unblockIDString = strconv.Itoa(int(id[0]))
	}
	// unblock is "AT_UNBLOCK0" or "AT_UNBLOCK<id>"

	// send a fake AT command to trigger some output ("COMMAND NOT SUPPORT")
	_, err := d.writer.Write(append([]byte("AT_UNBLOCK"+unblockIDString), d.eolSeq...))
	return err
}

// clearReader reads from device until all expected outputs have been found
// Acquires state lock as necessary, holds write lock
func (d *ScanWriter) clearReader(readData []byte) error {

	unblock := newClearUnblocker(d.unblockThreshold)
	go unblock.Run(d)
	defer unblock.Close()

	var numUnblocksSent unblockID
	var unblockIndexCleared unblockID = -1
	gotATUnblock := false // flag to keep track of wether last read line was AT_UNBLOCK
	expectCommandEcho := true
	if d.commandEcho == commandEchoDisabled {
		expectCommandEcho = false
	}
	count := 1
	for {

		fakeCmd := "AT_UNBLOCK" + strconv.Itoa(int(numUnblocksSent)) + string(d.eolSeq)
		state, err := d.inferState(readData, []byte(fakeCmd), count)
		if err != nil {
			return fmt.Errorf("error classifying device output: %v", err)
		}

		//log.Printf("DEBUG: clearReader: State: %s, data: '%v', lines: %d\nfakeCmd: %s\n", state, strings.ReplaceAll(strings.ReplaceAll(string(readData), "\r", "<CR>"), "\n", "<LF>"), count, fakeCmd)
		switch {
		case expectCommandEcho && state == ATEcho && !gotATUnblock && bytes.HasPrefix(readData, []byte("AT_UNBLOCK")):
			id, ok := isBytesUnblock(readData)
			if !ok {
				log.Printf("DEBUG: State: %s, data: '%v', lines: %d\nfakeCmd: %s\n", state, strings.ReplaceAll(strings.ReplaceAll(string(readData), "\r", "<CR>"), "\n", "<LF>"), count, fakeCmd)
				panic("ERROR - GOT AT UNBLOCK BUT COULD NOT PARSE ID")
			}
			unblockIndexCleared = id
			gotATUnblock = true
		case (gotATUnblock || (!gotATUnblock && !expectCommandEcho)) && bytes.HasPrefix(readData, []byte("COMMAND NOT SUPPORT")):
			gotATUnblock = false
			// if we have command echo, we will have been able to read the
			if unblockIndexCleared == numUnblocksSent {
				return nil
			}

			if !expectCommandEcho && numUnblocksSent > 1 {
				// if echo is off, we can't parse the echo for an index and can't be sure we've read all output
				// from the sent messages. Instead we assert that we've been blocked on atleast one Scan request.
				// This should imply we've read all the data there was to read.
				return nil
			}
			break
		default:
			// probably output beloning to the last command sent.
			d.recoverState(readData, count)
		}

		unblockReqID := unblock.Prepare()
		ok := d.reader.Scan()
		if !ok {
			// println("SCAN FAILED IN CLEAN") // i guess better luck next time?
		}
		cancelled := false
		numUnblocksSent, cancelled = unblock.Cancel(unblockReqID)
		if !cancelled {
			// a new unblock was sent, reset count
			count = 0
		}
		readData, err = d.reader.Bytes(), d.reader.Err()
		//log.Printf("DEBUG: clearReader NewData: '%s'\n", strings.ReplaceAll(strings.ReplaceAll(string(readData), "\r", "<CR>"), "\n", "<LF>"))
		if err != nil {
			return fmt.Errorf("error reading after context cancel: %v", readData)
		}
		count++
	}
}

// recoverState attempts to update state with data read after a read has been cancelled (and AT_UNBLOCK sent)
// Acquires state lock
// FIXME: duplicates some business logic from Scan()
func (d *ScanWriter) recoverState(readData []byte, outputCount int) {
	d.state.Lock()
	defer d.state.Unlock()
	//log.Printf("DEBUG: clearReader: State: %s, got extra data lines: '%v'\n", state, strings.ReplaceAll(strings.ReplaceAll(string(readData), "\r", "<CR>"), "\n", "<LF>"))
	state, err := d.inferState(readData, d.state.cmd, d.state.outputCount+outputCount)
	if err != nil {
		return
	}

	// TODO dechypher code and write some comments
	if state == ATEcho {
		d.state.current = []byte{}
		return
	}
	if d.state.outputCount+outputCount == 1 {
		// echo must be disabled. (future me: why? because it's the first line of output AND the state wasn't inferred to be Echo?)
		d.state.current = readData
		return
	}
	switch state {
	case ATError:
		d.state.cmdActive = false
	case ATReady:
		d.state.cmdActive = false
	}
	d.state.current = append(d.state.current, readData...)
}

type unblockRequestID int
type unblockID int
type clearUnblocker struct {
	prepareUnblock chan unblockRequestID
	cancelUnblock  chan unblockRequestID
	sentUnblock    chan unblockID // Buffer 1
	requestID      unblockRequestID
	threshold      time.Duration

	// cache
	lastUnlockID unblockID // note: this cannot be touched by Run()
}

func newClearUnblocker(timeout time.Duration) *clearUnblocker {
	c := clearUnblocker{
		prepareUnblock: make(chan unblockRequestID, 0),
		cancelUnblock:  make(chan unblockRequestID, 0),
		sentUnblock:    make(chan unblockID, 1),
		requestID:      0,
		threshold:      timeout,
	}
	return &c
}

// Run starts the unblocker process, waiting for unblock requests (via Prepare) and writes numbered AT_UNBLOCK's to supplied writer
// if the unblock request is not cancelled before timeout threshold is reached
// Requires exclusive access to writer during run
// Runs until Close is called
func (c *clearUnblocker) Run(swriter *ScanWriter) {
	swriter.writerLock.Lock() // hold the lock to ensure noone is writing while we are trying to clear the reader and sync up our state
	defer swriter.writerLock.Unlock()

	var unblockNumber unblockID = 1
unblock:
	for n := range c.prepareUnblock {
		// we race between a timeout and a cancel signal
	race:
		for {
			select {
			case <-time.After(c.threshold):
				err := swriter.cancelRead(uint(unblockNumber))
				// _, err := swriter.writer.Write([]byte(fmt.Sprintf("AT_UNBLOCK%d\r\n", unblockNumber)))
				if err != nil {
					println(err.Error()) // FIXME
					continue unblock
				}

				// order of these two are important
				c.sentUnblock <- unblockNumber
				<-c.cancelUnblock

				unblockNumber++
				continue unblock
			case m, ok := <-c.cancelUnblock:
				if !ok {
					return
				}
				if m == n {
					// cancelled
					continue unblock
				}
				// not the current unblock request that's getting cancelled. ignore it.
				continue race
			}
		}
	}
}

func (c *clearUnblocker) Prepare() unblockRequestID {
	id := c.requestID
	c.requestID++
	c.prepareUnblock <- id
	return id
}

// Cancel attempts to cancel the prepared unblock request.
// If cancel is called after the unblock request is dispatched, then the
func (c *clearUnblocker) Cancel(reqID unblockRequestID) (unblockID, bool) {
	c.cancelUnblock <- reqID

	select {
	case sentUnblockID, ok := <-c.sentUnblock:
		if !ok {
			break
		}
		// cancel request is too late - unblock request was sent
		c.lastUnlockID = sentUnblockID
		return c.lastUnlockID, false
	default:
	}
	return c.lastUnlockID, true
}

func (c *clearUnblocker) Close() {
	close(c.cancelUnblock)
	close(c.prepareUnblock)
	// c.sentUnblock closed in go routine exit
}

func isBytesUnblock(data []byte) (unblockID, bool) {
	if !bytes.HasPrefix(data, []byte("AT_UNBLOCK")) {
		return 0, false
	}
	line := bytes.TrimPrefix(data, []byte("AT_UNBLOCK"))
	breakIndex := bytes.Index(bytes.Replace(line, []byte("\r\r\n"), []byte("\r\n"), 1), []byte("\r\n"))
	if breakIndex < 0 {
		return -2, false
	}
	num := line[0:breakIndex]
	n, err := strconv.Atoi(string(num))
	if err != nil {
		return -2, false
	}
	return unblockID(n), true

}
