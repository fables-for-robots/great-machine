package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestErrorText(t *testing.T) {
	err := Errorf(CodeNotFound, "no service %q", "web")
	if got, want := err.Error(), `not-found: no service "web"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if err.Code != CodeNotFound || err.Message != `no service "web"` {
		t.Errorf("got %+v", err)
	}
}

func TestCodeOf(t *testing.T) {
	refusal := Errorf(CodeUnavailable, "not now")
	tests := []struct {
		name string
		err  error
		want Code
	}{
		{"nil", nil, ""},
		{"an Error", refusal, CodeUnavailable},
		{"a wrapped Error", fmt.Errorf("asking draal: %w", refusal), CodeUnavailable},
		{"wrapped twice", fmt.Errorf("a: %w", fmt.Errorf("b: %w", refusal)), CodeUnavailable},
		{"any other error", errors.New("disk full"), CodeInternal},
		{"the end of a stream", io.EOF, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeOf(tt.err); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteErrorAndReadResponse(t *testing.T) {
	refusal := Errorf(CodeUnauthorized, "%s may not roll out", "abc")
	tests := []struct {
		name string
		err  error
		want Error
	}{
		{"an Error", refusal, *refusal},
		{"a wrapped Error", fmt.Errorf("x: %w", refusal), *refusal},
		{"a plain error", errors.New("disk full"), Error{Code: CodeInternal, Message: "disk full"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteError(&buf, tt.err); err != nil {
				t.Fatalf("WriteError: %v", err)
			}
			resp, err := ReadResponse(&buf)
			if resp != nil {
				t.Errorf("ReadResponse returned %+v beside the error", resp)
			}
			var got *Error
			if !errors.As(err, &got) {
				t.Fatalf("ReadResponse: got %v (%T), want a *wire.Error", err, err)
			}
			if *got != tt.want {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
			if code := CodeOf(err); code != tt.want.Code {
				t.Errorf("CodeOf: got %q, want %q", code, tt.want.Code)
			}
		})
	}
}

func TestReadResponse(t *testing.T) {
	var buf bytes.Buffer
	want := &Response{Store: &StoreInfo{Server: "ab", Prefix: "gm-"}}
	if err := Write(&buf, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.Store == nil || *got.Store != *want.Store {
		t.Errorf("got %+v, want %+v", got.Store, want.Store)
	}
	if _, err := ReadResponse(&buf); err != io.EOF {
		t.Errorf("ReadResponse at the end: got %v, want io.EOF", err)
	}
}

// pipe is a stream whose peer is played by the test: what Call writes goes
// to sent, and what it reads comes from answer.
type pipe struct {
	sent   bytes.Buffer
	answer bytes.Buffer
}

func (p *pipe) Write(b []byte) (int, error) { return p.sent.Write(b) }
func (p *pipe) Read(b []byte) (int, error)  { return p.answer.Read(b) }

func TestCall(t *testing.T) {
	t.Run("an answer", func(t *testing.T) {
		var p pipe
		if err := Write(&p.answer, &Response{Rollout: &RolloutInfo{ID: 7, Service: "web"}}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		resp, err := Call(&p, &Request{Rollback: &RollbackRequest{Service: "web", Parallel: 2}})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if resp.Rollout == nil || resp.Rollout.ID != 7 {
			t.Errorf("got %+v", resp)
		}
		var sent Request
		if err := Read(&p.sent, &sent); err != nil {
			t.Fatalf("Read of what Call sent: %v", err)
		}
		if sent.Rollback == nil || *sent.Rollback != (RollbackRequest{Service: "web", Parallel: 2}) {
			t.Errorf("Call sent %+v", sent)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		var p pipe
		if err := WriteError(&p.answer, Errorf(CodeNotFound, "no service web")); err != nil {
			t.Fatalf("WriteError: %v", err)
		}
		resp, err := Call(&p, &Request{Rollback: &RollbackRequest{Service: "web"}})
		if resp != nil || CodeOf(err) != CodeNotFound {
			t.Errorf("got %+v, %v; want a not-found", resp, err)
		}
	})
	t.Run("no answer", func(t *testing.T) {
		var p pipe
		if _, err := Call(&p, &Request{Store: &StoreRequest{}}); err != io.EOF {
			t.Errorf("got %v, want io.EOF", err)
		}
	})
}
