package at

import (
	"context"
	"fmt"
	"io"
	"testing"
)

func TestScanWriter_Sequences(t *testing.T) {
	type fields struct {
		device        io.ReadWriter
		deviceProfile DeviceProfile
		current       []byte
		err           error
		cmd           []byte
		outputCount   int
		cmdActive     bool
	}
	type inputWithExpectedOutput struct {
		ctx context.Context

		input          string
		wantInputError bool

		// output to be appended to the test devices output buffer
		// note that nil []byte are converted to errors (so use empty []byte slices for empty outputs)
		output [][]byte

		skipScan        bool
		wantScanFailure bool

		wantState State
		// ignoreState means we don't care about the resulting state
		ignoreState bool
		wantData    string
		// ignoreData means we don't care about the response data sent
		ignoreData bool
		wantError  bool
	}
	tests := []struct {
		name         string
		fields       fields
		testSequence []inputWithExpectedOutput
	}{

		{
			name: "default state is ATReady",
			fields: fields{
				device: NewTestReadWriter(),
			},
			testSequence: []inputWithExpectedOutput{
				{
					skipScan:    true,
					wantState:   ATReady,
					ignoreState: false,
				},
			},
		},
		{
			name: "sending AT cycles states",
			fields: fields{
				device: NewTestReadWriter().
					WithWriteResult(nil).
					WithReadResult([]byte("AT\r\n"), nil).
					WithReadResult([]byte("OK\r\n"), nil),
			},
			testSequence: []inputWithExpectedOutput{
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
			name: "can write new command before previous has finished",
			fields: fields{
				device: NewTestReadWriter().
					WithWriteResult(nil). // AT
					WithReadResult([]byte("AT\r\n"), nil).
					WithWriteResult(nil). // ATA
					WithReadResult([]byte("ATA\r\n"), nil).
					WithReadResult([]byte("OK\r\n"), nil). // AT OK
					WithReadResult([]byte("OK\r\n"), nil), // ATA OK
			},
			testSequence: []inputWithExpectedOutput{
				{
					input:     "AT\r\n",
					wantState: ATEcho,
					wantData:  "AT\r\n",
				},
				{
					skipScan:  true,      // don't progress state and data
					input:     "ATA\r\n", // write a new command
					wantState: ATEcho,    // confirms data is still active from previous scan
					wantData:  "AT\r\n",
				},
				{
					wantState: ATData, // classification of the echo fails because we didnt wait for previous command to finish
					wantData:  "ATA\r\n",
				},
				{
					wantState: ATReady, // AT
					wantData:  "OK\r\n",
				},
				{
					wantState: ATReady, // ATA
					wantData:  "OK\r\n",
				},
			},
		},
		{
			name: "error in command correctly updates state",
			fields: fields{
				device: NewTestReadWriter().
					WithWriteResult(nil),
			},
			testSequence: []inputWithExpectedOutput{
				{
					input: "AT#VT\r\n",
					output: [][]byte{
						[]byte("AT#VT\r\n"),
						[]byte("ERROR: COMMAND NOT SUPPORT\r\n"),
						[]byte("\r\n"),
					},
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
				device: NewTestReadWriter().
					WithWriteResult(nil),
			},
			testSequence: []inputWithExpectedOutput{
				{
					input: "AT\r\n",
					output: [][]byte{
						[]byte("A"),
						nil,
						[]byte("T\r\n"),
						[]byte("OK\r\n"),
					},
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
				device: NewTestReadWriter().
					WithWriteResult(nil),
			},
			testSequence: []inputWithExpectedOutput{
				{
					input: "AT+CNUM\r\n",
					output: [][]byte{
						[]byte("AT+CNUM\r\n"),
						[]byte(`+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n"),
					},
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
				device: NewTestReadWriter().
					WithWriteResult(nil),
			},
			testSequence: []inputWithExpectedOutput{
				{
					input: "AT+CNUM\r\n",
					output: [][]byte{
						[]byte("AT+CNUM\r\n"),
						[]byte(`+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n"),
					},
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
					input: "AT\r\n",
					output: [][]byte{
						[]byte("AT\r\n"),
						[]byte(`OK` + "\r\n"),
					},
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
					wantData:  `OK` + "\r\n",
				},
			},
		},

		// Need to add a test profile to run this
		// {
		// 	name: "test device with inconsistent EOLs on Echoes",
		// 	fields: fields{
		// 		device: NewTestReadWriter().
		// 			WithWriteResult(nil),
		// 	},
		// 	testSequence: []inputWithExpectedOutput{
		// 		{
		// 			input: "AT+CNUM\r\n",
		// 			output: [][]byte{
		// 				[]byte("AT+CNUM\r\r\n"),
		// 				[]byte(`+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n"),
		// 				[]byte(`AT` + "\r\n"),
		// 				[]byte(`OK` + "\r\n"),
		// 			},
		// 			ignoreState:     false,
		// 			ignoreData:      true,
		// 			wantScanFailure: false,
		// 			wantError:       false,
		// 			wantState:       ATEcho,
		// 		},
		// 		{
		// 			wantError:  true,
		// 			wantState:  ATError,
		// 			wantData:   `+CME ERROR: invalid characters in text string+CNUM: "","+46123456789",145` + "\r\n",
		// 			ignoreData: false,
		// 		},
		// 	},
		// },
	}
	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sw := NewScanWriter(tt.fields.device, tt.fields.deviceProfile)

			sw.current = tt.fields.current
			sw.err = tt.fields.err
			sw.cmd = tt.fields.cmd
			sw.outputCount = tt.fields.outputCount
			sw.cmdActive = tt.fields.cmdActive

			for i, ts := range tt.testSequence {

				ctx := context.TODO()
				if ts.ctx != nil {
					ctx = ts.ctx
				}

				if ts.input != "" {
					if err := sw.Write([]byte(ts.input)); !ts.wantInputError && err != nil {
						t.Errorf("Part %d: ScanWriter.Write() = %v, wantInputError %v", i, err, ts.wantInputError)
					}
				}

				for _, buf := range ts.output {
					dev := tt.fields.device.(*TestReadWriter)
					if buf == nil {
						dev.WithReadResult(nil, fmt.Errorf("nil output buffer in test"))
						continue
					}
					dev.WithReadResult(buf, nil)
				}

				if !ts.skipScan {
					if scanOk := sw.Scan(ctx); scanOk == ts.wantScanFailure {
						t.Errorf("Part %d: ScanWriter.Scan() = %v, want %v", i, scanOk, !ts.wantScanFailure)
					}
				}
				if string(sw.Bytes()) != sw.String() {
					panic("Bytes() and String() not in sync")
				}

				if gotState := sw.State(); !ts.ignoreState && gotState != ts.wantState {
					t.Errorf("Part %d: ScanWriter.State() = %v, want %v", i, gotState, ts.wantState)
				}

				if gotString := sw.String(); !ts.ignoreData && gotString != ts.wantData {
					t.Errorf("Part %d: ScanWriter.Bytes() = %v, want %v", i, gotString, ts.wantData)
				}

				if gotError := sw.Err(); (!ts.wantError && gotError != nil) || (ts.wantError && gotError == nil) {
					t.Errorf("Part %d: ScanWriter.Err() = %v, wantError %v", i, gotError, ts.wantError)
				}

			}

		})
	}
}
