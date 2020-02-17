package at

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func timeoutCtx(t time.Duration) func() context.Context {
	return func() context.Context {
		ctx, _ := context.WithTimeout(context.Background(), t)
		return ctx
	}
}

func TestScanWriter_FakeDevice(t *testing.T) {
	type fields struct {
		deviceEmu        func(writes <-chan WriteRequest, reads <-chan ReadRequest) error
		deviceProfile    DeviceProfile
		current          []byte        // internal state of scanWriter
		err              error         // internal error of scanWriter
		cmd              []byte        // name of the last command sent on the scanWriter
		outputCount      int           // number of lines of output read since sending the last command
		cmdActive        bool          // wether the last sent command has been determined to have finished
		unblockThreshold time.Duration // time before additional unblocks are sent in clearReader
	}
	type writeAndScanParams struct {
		// Write
		ctxFactory     func() context.Context
		input          string
		wantInputError bool

		// Scan
		skipScan        bool
		wantScanFailure bool
		wantState       State
		ignoreState     bool // ignoreState means we don't care about the resulting state
		wantData        string
		ignoreData      bool // ignoreData means we don't care about the response data sent
		wantError       bool
	}
	tests := []struct {
		name         string
		fields       fields
		testSequence []writeAndScanParams
	}{
		{
			name: "AT_UNBLOCK is sent when context expires during scan",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes                 // Assume AT was written
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads // Test proceeds to Scan which triggers a Read
					// Block read until we get the AT_UNBLOCK

					wreq2 := <-writes                         // Assume AT_UNBLOCK was written
					wreq2.Response <- WriteResponse{Err: nil} // signal write as OK

					rreq.Response <- ReadResponse{OutData: []byte("AT_UNBLOCK0\r\nCOMMAND NOT SUPPORT\r\n")} // Fake device echo and output

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					ctxFactory:      timeoutCtx(time.Millisecond),
					input:           "AT\r\n",
					ignoreState:     true,
					ignoreData:      true,
					wantScanFailure: true,
					wantError:       true,
					wantState:       ATEcho,
					wantData:        "AT+CNUM\r\n",
				},
			},
		},
		{
			name: "AT_UNBLOCK with ATE0",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes                 // Assume ATE0 was written
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("OK\r\n")}

					wreq = <-writes                          // Assume a command was written
					wreq.Response <- WriteResponse{Err: nil} // signal write as OK

					rreq = <-reads // block on scan for the written command

					wreq = <-writes                          // Assume AT_UNBLOCK0 was written
					wreq.Response <- WriteResponse{Err: nil} // signal write as OK

					// send AT_UNBLOCK response without echo
					rreq.Response <- ReadResponse{OutData: []byte("COMMAND NOT SUPPORT\r\n")}

					wreq = <-writes                          // Assume AT_UNBLOCK1 was written
					wreq.Response <- WriteResponse{Err: nil} // signal write as OK

					// send AT_UNBLOCK response without echo
					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("COMMAND NOT SUPPORT\r\n")}

					wreq = <-writes                          // Assume AT_UNBLOCK2 was written
					wreq.Response <- WriteResponse{Err: nil} // signal write as OK

					// send AT_UNBLOCK response without echo
					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("COMMAND NOT SUPPORT\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:     "ATE0\r\n",
					wantState: ATReady,
					wantData:  "OK\r\n",
				},
				{
					ctxFactory:      timeoutCtx(time.Millisecond),
					input:           "AT\r\n",
					ignoreState:     true,
					ignoreData:      true,
					wantScanFailure: true,
					wantError:       true,
				},
			},
		},
		{
			name: "AT_UNBLOCK with staggered output",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes                 // Assume AT was written
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads // Test proceeds to Scan which triggers a Read
					// Block read until we get the AT_UNBLOCK

					wreq2 := <-writes                         // Assume AT_UNBLOCK was written
					wreq2.Response <- WriteResponse{Err: nil} // signal write as OK

					rreq.Response <- ReadResponse{OutData: []byte("AT_UNB")}
					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("LOCK0\r\nCOMMAND NOT SUPPORT\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					ctxFactory:      timeoutCtx(time.Millisecond),
					input:           "AT\r\n",
					ignoreState:     true,
					ignoreData:      true,
					wantScanFailure: true,
					wantError:       true,
					wantState:       ATEcho,
					wantData:        "AT+CNUM\r\n",
				},
			},
		},
		{
			name: "AT_UNBLOCK0 response gets eaten",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // Assume ATD123123131231 was written
					orgCmd := string(wreq.InData)
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads // Test proceeds to Scan which triggers a Read
					// Block read until we get the AT_UNBLOCK

					wreq = <-writes                          // Assume AT_UNBLOCK was written
					wreq.Response <- WriteResponse{Err: nil} // signal write as OK

					// send original command echo and OK instead of AT_UNBLOCK
					rreq.Response <- ReadResponse{OutData: []byte(orgCmd + "OK\r\n")}

					// now our cleanup routine should be stuck waiting for AT_UNBLOCK, which isnt going to arrive.
					// Then it should send a secondary AT_UNBLOCK
					wreq = <-writes
					// wreq.InData // expect AT_UNBLOCK_1
					if string(wreq.InData) != "AT_UNBLOCK1\r\n" {
						return fmt.Errorf("expected write to be AT_UNBLOCK1, not '%s'", wreq.InData)
					}
					wreq.Response <- WriteResponse{} // ok

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT_UNBLOCK1\r\nCOMMAND NOT SUPPORT\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					ctxFactory:      timeoutCtx(time.Millisecond),
					input:           "ATD123213131231\r\n",
					ignoreState:     true,
					ignoreData:      true,
					wantScanFailure: true,
					wantError:       true,
					wantState:       ATEcho,
					wantData:        "OK\r\n",
				},
			},
		},
		{
			name: "AT_UNBLOCK with ATE0 get swallowed",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // Assume ATD123123131231 was written
					orgCmd := string(wreq.InData)
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads // Test proceeds to Scan which triggers a Read
					// Block read until we get the AT_UNBLOCK

					wreq = <-writes                  // Assume AT_UNBLOCK was written
					wreq.Response <- WriteResponse{} // signal write as OK

					// send original command echo and OK instead of AT_UNBLOCK
					rreq.Response <- ReadResponse{OutData: []byte(orgCmd + "OK\r\n")}

					// now our cleanup routine should be stuck waiting for AT_UNBLOCK, which isnt going to arrive.
					// Then it should send a secondary AT_UNBLOCK
					wreq = <-writes
					// wreq.InData // expect AT_UNBLOCK_1
					if string(wreq.InData) != "AT_UNBLOCK1\r\n" {
						return fmt.Errorf("expected write to be AT_UNBLOCK1, not '%s'", wreq.InData)
					}
					wreq.Response <- WriteResponse{} // ok

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT_UNBLOCK1\r\nCOMMAND NOT SUPPORT\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					ctxFactory:      timeoutCtx(time.Millisecond),
					input:           "ATD123213131231\r\n",
					ignoreState:     true,
					ignoreData:      false,
					wantScanFailure: true,
					wantError:       true,
					wantState:       ATEcho,
					wantData:        "OK\r\n",
				},
			},
		},
		{
			name: "default state is ATReady",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					skipScan:    true,
					wantState:   ATReady,
					ignoreState: false,
				},
			},
		},
		{
			name: "sending AT cycles state",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // AT
					wreq.Response <- WriteResponse{}

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT\r\n")}

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("OK\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:     "AT\r\n",
					wantState: ATEcho,
					wantData:  "AT\r\n",
				},
				{
					wantState: ATReady,
					wantData:  "OK\r\n",
				},
			},
		},
		{
			name: "cannot write command before previous has finished",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // AT
					wreq.Response <- WriteResponse{}

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT\r\n")}

					// attempt to write second command fails before hitting device
					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:     "AT\r\n",
					wantState: ATEcho,
					wantData:  "AT\r\n",
				},
				{
					skipScan:       true,      // don't progress state and data
					input:          "ATA\r\n", // write a new command
					wantInputError: true,
					ignoreState:    true,
					ignoreData:     true,
				},
			},
		},
		{
			name: "error in command correctly updates state",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes                 // AT#VT
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq := <-reads // Test proceeds to Scan which triggers a Read
					rreq.Response <- ReadResponse{OutData: []byte("AT#VT\r\n")}

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("ERROR: COMMAND NOT SUPPORT\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:     "AT#VT\r\n",
					wantState: ATEcho,
					wantData:  "AT#VT\r\n",
				},
				{
					wantState: ATError,
					wantData:  "ERROR: COMMAND NOT SUPPORT\r\n",
					wantError: true,
				},
			},
		},
		{
			name: "device read errors propagate",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // AT
					wreq.Response <- WriteResponse{}

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("A")}
					rreq = <-reads
					rreq.Response <- ReadResponse{Err: fmt.Errorf("fake device error")}
					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("T\r\n")}
					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("OK\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:           "AT\r\n",
					ignoreState:     true,
					ignoreData:      true,
					wantScanFailure: true,
					wantError:       true,
				},
				{
					wantState: ATEcho,
					wantData:  "AT\r\n",
				},
				{
					wantState: ATReady,
					wantData:  "OK\r\n",
				},
			},
		},
		{
			name: "test AT error condition",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // AT+CNUM
					wreq.Response <- WriteResponse{}

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT+CNUM\r\n")}

					rreq = <-reads // simulates the weird behaviour exhibited by the K3520
					rreq.Response <- ReadResponse{OutData: []byte(`+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:           "AT+CNUM\r\n",
					ignoreState:     false,
					ignoreData:      true,
					wantScanFailure: false,
					wantError:       false,
					wantState:       ATEcho,
				},
				{
					wantError:  true,
					wantState:  ATError,
					wantData:   `+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n",
					ignoreData: false,
				},
			},
		},
		{
			name: "test that Bytes always returns the raw bytes read",
			fields: fields{
				deviceEmu: func(writes <-chan WriteRequest, reads <-chan ReadRequest) error {
					wreq := <-writes // AT+CNUM
					wreq.Response <- WriteResponse{}

					rreq := <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT+CNUM\r\n")}

					rreq = <-reads // simulates the weird behaviour exhibited by the K3520
					rreq.Response <- ReadResponse{OutData: []byte(`+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n")}

					wreq = <-writes                  // AT
					wreq.Response <- WriteResponse{} // signal write as OK

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("AT\r\n")}

					rreq = <-reads
					rreq.Response <- ReadResponse{OutData: []byte("OK\r\n")}

					return nil
				},
			},
			testSequence: []writeAndScanParams{
				{
					input:           "AT+CNUM\r\n",
					ignoreState:     false,
					ignoreData:      true,
					wantScanFailure: false,
					wantError:       false,
					wantState:       ATEcho,
					wantData:        "AT+CNUM\r\n",
				},
				{
					wantError: true,
					wantState: ATError,
					wantData:  `+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n",
				},
				{
					input:           "AT\r\n",
					ignoreState:     false,
					ignoreData:      true,
					wantScanFailure: false,
					wantError:       false,
					wantState:       ATEcho,
					wantData:        "AT\r\n",
				},
				{
					wantError: false,
					wantState: ATReady,
					wantData:  "OK\r\n",
				},
			},
		},
	}
	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			device, deviceErrChan := NewFakeDeviceReadWriter(tt.fields.deviceEmu)
			sw := newScanWriter(device, tt.fields.deviceProfile, false)
			sw.unblockThreshold = tt.fields.unblockThreshold
			sw.state.current = tt.fields.current
			sw.state.err = tt.fields.err
			sw.state.cmd = tt.fields.cmd
			sw.state.outputCount = tt.fields.outputCount
			sw.state.cmdActive = tt.fields.cmdActive

			if sw.unblockThreshold == 0 {
				sw.unblockThreshold = time.Millisecond
			}

			func() {
				defer device.Close()
				for i, ts := range tt.testSequence {

					if ts.input != "" {
						if err := sw.Write([]byte(ts.input)); !ts.wantInputError && err != nil {
							t.Errorf("Part %d: ScanWriter.Write() = %v, wantInputError %v", i, err, ts.wantInputError)
							return
						}
					}

					ctx := context.TODO()
					if ts.ctxFactory != nil {
						ctx = ts.ctxFactory()
					}

					if !ts.skipScan {
						if scanOk := sw.Scan(ctx); scanOk == ts.wantScanFailure {
							t.Errorf("Part %d: ScanWriter.Scan() = %v, want %v", i, scanOk, !ts.wantScanFailure)
							return
						}
					}
					if string(sw.Bytes()) != sw.String() {
						panic("Bytes() and String() not in sync")
					}

					if gotState := sw.State(); !ts.ignoreState && gotState != ts.wantState {
						t.Errorf("Part %d: ScanWriter.State() = %v, want %v", i, gotState, ts.wantState)
						return
					}

					if gotString := sw.String(); !ts.ignoreData && gotString != ts.wantData {
						t.Errorf("Part %d: ScanWriter.Bytes() = %v, want %v", i, gotString, ts.wantData)
						return
					}

					if gotError := sw.Err(); (!ts.wantError && gotError != nil) || (ts.wantError && gotError == nil) {
						t.Errorf("Part %d: ScanWriter.Err() = %v, wantError %v", i, gotError, ts.wantError)
						return
					}
				}
				device.Close()
				deviceErr := <-deviceErrChan
				if deviceErr != nil {
					t.Errorf("Device Emulator error = %v", deviceErr)
					return
				}
			}()

		})
	}
}
