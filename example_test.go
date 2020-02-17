package at_test

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/flipb/at"
)

func openATDevice() io.ReadWriter {
	fakeDevice, _ := at.NewFakeDeviceReadWriter(func(writes <-chan at.WriteRequest, reads <-chan at.ReadRequest) error {
		lastWrite := ""
		for {
			select {
			case wreq, ok := <-writes:
				if !ok {
					return nil
				}
				lastWrite = string(wreq.InData)
				wreq.Response <- at.WriteResponse{} // responding with empty WriteResponse means Write was accepted by the fake device
			case rreq, ok := <-reads:
				if !ok {
					return nil
				}
				// Always return OK when someone is reading from device, except if something was just written, then return what was just written
				data := "OK\r\n"
				if lastWrite != "" {
					data = lastWrite
					lastWrite = ""
				}
				rreq.Response <- at.ReadResponse{OutData: []byte(data)}
			}
		}
	})

	return fakeDevice
}

func ExampleNewAT() {

	ctx := context.Background()

	var rawDevice io.ReadWriter = openATDevice()

	// create AT - note that specifying the DeviceProfile is optional
	atDevice := at.NewAT(rawDevice, at.WithProfile(at.DefaultProfile{}))

	// we want to set a timeout for the AT command we're about to send.
	commandCtx, cancelFn := context.WithTimeout(ctx, time.Second*10)
	defer cancelFn()

	// Send the command "AT". Implementation of Send adds EOL automatically.
	response, err := atDevice.Send(commandCtx, "AT")
	if err != nil {
		// there was an error sending the command.
		// Could be io error, context timeout, or some AT error
		switch err {
		case at.ErrUnexpectedPrompt:
			// handle
		case context.DeadlineExceeded:
			// handle
		default:
			panic(err)
		}
	}

	// Print the response
	println(response)
	// NOTE: response here should be an empty string
	// this is because command echoes, "OK" and error strings are stripped from the response.
	// error strings are instead put in the err return value

	// so, again, response is empty for AT commands that don't output anything (other than potential command echo and OK)
}

func ExampleNewScanWriter() {
	ctx := context.Background()

	var rawDevice io.ReadWriter = openATDevice()

	// create a scanwriter over an io.ReadWriter
	// after this point you should no longer use the rawDevice anywhere else
	scanwriter := at.NewScanWriter(rawDevice, at.DefaultProfile{})

	// initial state should be "ready"
	state := scanwriter.State()
	if state != at.ATReady {
		panic("unexpected state")
	}

	scanContext, cancelFn := context.WithTimeout(ctx, time.Second*10)
	defer cancelFn()

	// in order to update scanwriter's state we have to either Scan or Write.
	// Scan will block until there's enough output from the device, or until context is cancelled.
	ok := scanwriter.Scan(scanContext)
	// we expect "ok" to be false in this case as no output should have been written
	// Scan only returned because the context timed out
	if !ok {
		// let's confirm our theory
		err := scanwriter.Err()
		switch err {
		case context.DeadlineExceeded:
			// well this was expected!
		default:
			panic("unexpected error")
		}
	} else {
		panic("unexpected scan result")
	}

	// write a command to the AT device
	err := scanwriter.Write([]byte("AT\r\n"))
	if err != nil {
		panic("error writing to scanwriter")
	}

	states := 0
	// scan in a loop
	for scanwriter.Scan(ctx) {
		state := scanwriter.State()
		switch state {
		// We expect the first output after sending out command to be an Echo
		case at.ATEcho:
			states++
			continue
		case at.ATData:
			// we got data - this is unexpected for the AT command
			// AT normally only returns an echo (ATEcho) and an OK (ATReady)

			// to get the data returned
			data := scanwriter.Bytes()
			println(data)

			// String is the same as Bytes, only with a type conversion built in.
			_ = scanwriter.String()

			continue
		case at.ATError:
			// ATError means the command failed and returned an error

			// lets see the error
			err = scanwriter.Err()
			fmt.Printf("error in AT command: %v\n", err)

			// let's also inspect if the AT command returned any data before the error occurred
			data := scanwriter.Bytes()
			fmt.Printf("AT command output: %s", data)

			break

		case at.ATNotice:
			// notice is very unexpected here - normally only sent on the Notification port on my device.
			// An ATNotice is a notification pushed from the network, ie. ^BOOT, RING, etc.
			// ATNotice and ATData classification might get be mixed up.
		case at.ATReady:
			// we got an "OK" - command finished successfully
			println("Command completed successfully!")
			break
		}
	}
	err = scanwriter.Err()
	if err != nil {
		panic("error scanning")
	}

}
