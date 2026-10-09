package wire

import (
	"errors"
	"fmt"
	"io"
)

// Code says what kind of refusal an Error is.
type Code string

// The codes of a refusal.
const (
	// CodeBadRequest: the request itself is wrong, and will be wrong again.
	CodeBadRequest Code = "bad-request"
	// CodeUnauthorized: the peer may not do this.
	CodeUnauthorized Code = "unauthorized"
	// CodeNotFound: what the request names is not there.
	CodeNotFound Code = "not-found"
	// CodeUnavailable: it cannot be done now, and may be possible later.
	CodeUnavailable Code = "unavailable"
	// CodeInternal: the answering side failed.
	CodeInternal Code = "internal"
)

// Error is a refusal, as it travels.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Error returns the code and the message as "<code>: <message>".
func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Errorf returns an Error with the code and the formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the code of the *Error in err's chain, CodeInternal for
// any other error and "" for nil.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// WriteError writes a Response that carries err. An error that has no
// *Error in its chain is written as CodeInternal with its text. One that
// has is written as that *Error, without what was wrapped around it.
func WriteError(w io.Writer, err error) error {
	var e *Error
	if !errors.As(err, &e) {
		text := "no error"
		if err != nil {
			text = err.Error()
		}
		e = &Error{Code: CodeInternal, Message: text}
	}
	return Write(w, &Response{Error: e})
}

// ReadResponse reads one Response. A Response that carries an Error is
// returned as that *Error. The end of r is returned as Read returns it.
func ReadResponse(r io.Reader) (*Response, error) {
	var resp Response
	if err := Read(r, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return &resp, nil
}

// Call writes req and reads one Response, as ReadResponse does.
func Call(s io.ReadWriter, req *Request) (*Response, error) {
	if err := Write(s, req); err != nil {
		return nil, err
	}
	return ReadResponse(s)
}
