// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package errors defines the error handling used by all Upspin software.
package errors

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"

	"upspin.io/log"
	"upspin.io/upspin"
)

// Error is the type that implements the error interface.
// It contains a number of fields, each of different type.
// An Error value may leave some values unset.
type Error struct {
	// Path is the Upspin path name of the item being accessed.
	Path upspin.PathName
	// User is the Upspin name of the user attempting the operation.
	User upspin.UserName
	// Op is the operation being performed, usually the name of the method
	// being invoked (Get, Put, etc.). It should not contain an at sign @.
	Op string
	// Kind is the class of error, such as permission failure,
	// or "Other" if its class is unknown or irrelevant.
	Kind Kind
	// The underlying error that triggered this one, if any.
	Err error
}

var (
	_ error                      = (*Error)(nil)
	_ encoding.BinaryUnmarshaler = (*Error)(nil)
	_ encoding.BinaryMarshaler   = (*Error)(nil)
)

// Kind defines the kind of error this is, mostly for use by systems
// such as FUSE that must act differently depending on the error.
type Kind uint8

const (
	Other      Kind = iota // Unclassified error. This value is not printed in the error message.
	Invalid                // Invalid operation for this type of item.
	Permission             // Permission denied.
	Syntax                 // Ill-formed argument such as invalid file name.
	IO                     // External I/O error such as network failure.
	Exist                  // Item exists but should not.
	NotExist               // Item does not exist.
)

func (k Kind) String() string {
	switch k {
	case Invalid:
		return "invalid operation"
	case Permission:
		return "permission denied"
	case Syntax:
		return "syntax error"
	case IO:
		return "I/O error"
	case Exist:
		return "item already exists"
	case NotExist:
		return "item does not exist"
	case Other:
		return "other error"
	}
	return "unknown error kind"
}

// E builds an error value from its arguments.
// The type of each argument determines its meaning.
// If more than one argument of a given type is presented,
// only the last one is recorded.
//
// The types are:
//	upspin.PathName
//		The Upspin path name of the item being accessed.
//	upspin.UserName
//		The Upspin name of the user attempting the operation.
//	string
//		The operation being performed, usually the method
//		being invoked (Get, Put, etc.)
//	errors.Kind
//		The class of error, such as permission failure.
//	error
//		The underlying error that triggered this one.
//
// If the error is printed, only those items that have been
// set to non-zero values will appear in the result.
//
func E(args ...interface{}) error {
	if len(args) == 0 {
		return nil
	}
	e := &Error{}
	for _, arg := range args {
		switch arg := arg.(type) {
		case upspin.PathName:
			e.Path = arg
		case upspin.UserName:
			e.User = arg
		case string:
			// Someone might accidentally call us with a user or path name
			// that is not of the right type. Take care of that and log it.
			if strings.Contains(arg, "@") {
				_, file, line, _ := runtime.Caller(1)
				log.Printf("errors.E: unqualified type for %q from %s:%d", arg, file, line)
				if strings.Contains(arg, "/") {
					if e.Path != "" { // Don't overwrite a valid path.
						e.Path = upspin.PathName(arg)
					}
				} else {
					if e.User != "" { // Don't overwrite a valid user.
						e.User = upspin.UserName(arg)
					}
				}
				continue
			}
			e.Op = arg
		case Kind:
			e.Kind = arg
		case error:
			e.Err = arg
		default:
			_, file, line, _ := runtime.Caller(1)
			log.Printf("errors.E: bad call from %s:%d: %v", file, line, args)
			return fmt.Errorf("unknown type %T, value %v in error call", arg, arg)
		}
	}
	return e
}

// pad appends str to the buffer if the buffer already has some data.
func pad(b *bytes.Buffer, str string) {
	if b.Len() == 0 {
		return
	}
	b.WriteString(str)
}

func (e *Error) Error() string {
	b := new(bytes.Buffer)
	if e.Path != "" {
		b.WriteString(string(e.Path))
	}
	if e.User != "" {
		pad(b, ", ")
		b.WriteString("for ")
		b.WriteString(string(e.User))
	}
	if e.Op != "" {
		pad(b, ": ")
		b.WriteString(e.Op)
	}
	if e.Kind != 0 {
		pad(b, ": ")
		b.WriteString(e.Kind.String())
	}
	if e.Err != nil {
		// Indent on new line if we are cascading Upspin errors.
		if _, ok := e.Err.(*Error); ok {
			pad(b, ":\n\t")
		} else {
			pad(b, ": ")
		}
		b.WriteString(e.Err.Error())
	}
	if b.Len() == 0 {
		return "no error"
	}
	return b.String()
}

// Recreate the errors.New functionality of the standard Go errors package
// so we can create simple text errors when needed.

// Str returns an error that formats as the given text. It is intended to
// be used as the error-typed argument to the E function.
func Str(text string) error {
	return &errorString{text}
}

// errorString is a trivial implementation of error.
type errorString struct {
	s string
}

func (e *errorString) Error() string {
	return e.s
}

// MarshalAppend marshals err into a byte slice. The result is appended to b,
// which may be nil.
// It returns the argument slice unchanged if the error is nil.
func (err *Error) MarshalAppend(b []byte) []byte {
	if err == nil {
		return b
	}
	b = appendString(b, string(err.Path))
	b = appendString(b, string(err.User))
	b = appendString(b, err.Op)
	var tmp [16]byte // For use by PutVarint.
	N := binary.PutVarint(tmp[:], int64(err.Kind))
	b = append(b, tmp[:N]...)
	b = MarshalErrorAppend(err.Err, b)
	return b
}

// Marshal marshals its receiver into a byte slice, which it returns.
// It returns nil if the error is nil. The returned error is always nil.
func (err *Error) MarshalBinary() ([]byte, error) {
	return err.MarshalAppend(nil), nil
}

// MarshalErrorAppend marshals an arbitrary error into a byte slice.
// The result is appended to b, which may be nil.
// It returns the argument slice unchanged if the error is nil.
// If the error is not an *Error, it just records the result of err.Error().
// Otherwise it encodes the full Error struct.
func MarshalErrorAppend(err error, b []byte) []byte {
	if err == nil {
		return b
	}
	if e, ok := err.(*Error); ok {
		// This is an errors.Error. Mark it as such.
		b = append(b, 'E')
		return e.MarshalAppend(b)
	}
	// Ordinary error.
	b = append(b, 'e')
	b = appendString(b, err.Error())
	return b

}

// MarshalError marshals an arbitrary error and returns the byte slice.
// If the error is nil, it returns nil.
// It returns the argument slice unchanged if the error is nil.
// If the error is not an *Error, it just records the result of err.Error().
// Otherwise it encodes the full Error struct.
func MarshalError(err error) []byte {
	return MarshalErrorAppend(err, nil)
}

// UnmarshalBinary unmarshals the byte slice into the receiver, which must be non-nil.
// The returned error is always nil.
func (err *Error) UnmarshalBinary(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	data, b := getBytes(b)
	if data != nil {
		err.Path = upspin.PathName(data)
	}
	data, b = getBytes(b)
	if data != nil {
		err.User = upspin.UserName(data)
	}
	data, b = getBytes(b)
	if data != nil {
		err.Op = string(data)
	}
	k, N := binary.Varint(b)
	err.Kind = Kind(k)
	b = b[N:]
	err.Err = UnmarshalError(b)
	return nil
}

// UnmarshalError unmarshals the byte slice into an error value.
// The byte slice must have been created by MarshalError or
// MarshalErrorAppend.
// If the encoded error was of type *Error, the returned error value
// will have that underlying type. Otherwise it will be just a simple
// value that implements the error interface.
func UnmarshalError(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	code := b[0]
	b = b[1:]
	switch code {
	case 'e':
		// Plain error.
		var data []byte
		data, b = getBytes(b)
		if len(b) != 0 {
			log.Printf("Unmarshal error: trailing bytes")
		}
		return Str(string(data))
	case 'E':
		// Error value.
		var err Error
		err.UnmarshalBinary(b)
		return &err
	default:
		log.Printf("Unmarshal error: corrup data %q", b)
		return Str(string(b))
	}
}

func appendString(b []byte, str string) []byte {
	var tmp [16]byte // For use by PutUvarint.
	N := binary.PutUvarint(tmp[:], uint64(len(str)))
	b = append(b, tmp[:N]...)
	b = append(b, str...)
	return b
}

// getBytes unmarshals the byte slice at b (uvarint count followed by bytes)
// and returns the slice followed by the remaining bytes.
// If there is insufficient data, both return values will be nil.
func getBytes(b []byte) (data, remaining []byte) {
	u, N := binary.Uvarint(b)
	if len(b) < N+int(u) {
		log.Printf("Unmarshal error: bad encoding")
		return nil, nil
	}
	if N == 0 {
		log.Printf("Unmarshal error: bad encoding")
		return nil, b
	}
	return b[N : N+int(u)], b[N+int(u):]
}
