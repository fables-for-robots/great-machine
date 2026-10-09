package wire

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

// fill sets v, and everything in it, to something that is not its zero
// value: every pointer points, every slice has two elements, every field
// of every struct is set. A time is in UTC, as Read returns it, so that
// reflect.DeepEqual compares what was read with what was written.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Interface().(type) {
	case time.Time:
		v.Set(reflect.ValueOf(time.Date(2026, 10, 9, 8, 7, 6, 123456789, time.UTC)))
		return
	case time.Duration:
		v.SetInt(int64(90*time.Second + 7*time.Nanosecond))
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fill(t, v.Field(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := 0; i < v.Len(); i++ {
			fill(t, v.Index(i))
		}
	case reflect.String:
		v.SetString("text \u00e9")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(-7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(200)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.25)
	default:
		t.Fatalf("fill: a field of kind %s, which this test does not know how to set", v.Kind())
	}
}

// roundTripEachField writes a message of the type of zero once for each of
// its fields, with that field alone set and everything in it set, and
// expects to read the same.
func roundTripEachField(t *testing.T, zero any) {
	typ := reflect.TypeOf(zero)
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			want := reflect.New(typ)
			fill(t, want.Elem().Field(i))

			var buf bytes.Buffer
			if err := Write(&buf, want.Interface()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := reflect.New(typ)
			if err := Read(&buf, got.Interface()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Errorf("got  %+v\nwant %+v", got.Elem().Field(i).Elem(), want.Elem().Field(i).Elem())
			}
		})
	}
}

func TestEveryRequestRoundTrips(t *testing.T) {
	roundTripEachField(t, Request{})
}

func TestEveryResponseRoundTrips(t *testing.T) {
	roundTripEachField(t, Response{})
}

func TestFramesThatAreNoResponseRoundTrip(t *testing.T) {
	// A State is a frame of its own after Registered, and a ShellInput
	// after the first ShellOutput.
	for _, zero := range []any{State{}, ShellInput{}} {
		typ := reflect.TypeOf(zero)
		t.Run(typ.Name(), func(t *testing.T) {
			want := reflect.New(typ)
			fill(t, want.Elem())
			var buf bytes.Buffer
			if err := Write(&buf, want.Interface()); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := reflect.New(typ)
			if err := Read(&buf, got.Interface()); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Errorf("got  %+v\nwant %+v", got.Elem(), want.Elem())
			}
		})
	}
}

func TestAnEmptyAnswerIsStillSet(t *testing.T) {
	// Registered, Prepared and Activated have no fields. The answer is
	// told from no answer by the pointer alone.
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Prepared: &Prepared{}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Prepared == nil {
		t.Error("Prepared is nil after the round trip")
	}
	if got.Registered != nil || got.Activated != nil || got.Error != nil {
		t.Errorf("more than Prepared is set: %+v", got)
	}
}

func TestShellExitOfZeroIsStillSet(t *testing.T) {
	zero := 0
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Shell: &ShellOutput{Exit: &zero}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Shell == nil || got.Shell.Exit == nil || *got.Shell.Exit != 0 {
		t.Errorf("got %+v, want an exit status of 0 that is set", got.Shell)
	}
}

func TestLogLineTextRoundTrips(t *testing.T) {
	texts := []string{
		"plain",
		"",
		"the replacement character \ufffd, which the ring puts where bytes were no UTF-8",
		"\ufffd\ufffd\ufffd",
		"a NUL \x00 and an escape \x1b[31m and a bell \a",
		"a tab\tand a carriage return\r",
		"two bytes \u00e9, three \u20ac, four \U0001f600",
		"the last code point \U0010ffff and the first of the private use \ue000",
		"a byte order mark \ufeff and a noncharacter \uffff",
		"combining e\u0301 and right-to-left \u202e",
	}
	for _, text := range texts {
		var buf bytes.Buffer
		if err := Write(&buf, &Response{Line: &LogLine{Stream: "stdout", Text: text}}); err != nil {
			t.Fatalf("Write of %q: %v", text, err)
		}
		got, err := ReadResponse(&buf)
		if err != nil {
			t.Fatalf("ReadResponse of %q: %v", text, err)
		}
		if got.Line == nil || got.Line.Text != text {
			t.Errorf("got %+v, want the text %q", got.Line, text)
		}
	}
}

// Text that is no valid UTF-8 is written without complaint and refused by
// the reader, with the whole message. This is why logring replaces such
// bytes before a line gets its text: a single one would end the stream of
// whoever follows the log.
func TestTextThatIsNoUTF8IsRefusedByTheReader(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, &Response{Line: &LogLine{Stream: "stdout", Text: "a\xffb"}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := ReadResponse(&buf); err == nil {
		t.Error("ReadResponse of a text that is no UTF-8: no error")
	}
}

func TestRolloutStateUnderWay(t *testing.T) {
	tests := []struct {
		state RolloutState
		want  bool
	}{
		{RolloutPreparing, true},
		{RolloutActivating, true},
		{RolloutCompleted, false},
		{RolloutHalted, false},
		{RolloutSuperseded, false},
		{"", false},
		{"unknown", false},
	}
	for _, tt := range tests {
		if got := tt.state.UnderWay(); got != tt.want {
			t.Errorf("%q.UnderWay(): got %v, want %v", tt.state, got, tt.want)
		}
	}
}
