// Package errors defines the error handling used by all Upspin software.
package errors

import (
	"bytes"
	"fmt"
	"runtime"

	"upspin.io/log"
	"upspin.io/upspin"
)

// e is the type that implements the error interface.
// It contains a number of fields, each of different type.
type Error struct {
	Path   upspin.PathName
	User   upspin.UserName
	Method string
	Class  Class
	Err    error
}

var _ error = (*Error)(nil)

// Class defines the kind of error this is, mostly for use by systems
// such as FUSE that must act differently depending on the error.
type Class uint8

const (
	NoError    Class = iota // No error.
	Invalid                 // Invalid operation for this type of item.
	Permission              // Permission denied.
	Syntax                  // Ill-formed argument such as invalid file name.
	IO                      // External IO error such as network failure.
	Exist                   // Item exists but should not.
	NotExist                // Item does not exist.
	Other                   // Some other error.
)

func (c Class) String() string {
	switch c {
	case NoError:
		return "no error"
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
	return "unknown error class"
}

// E builds an error value from its arguments.
// The type of each argument determines its meaning.
// Only one argument of each type may be present (if
// there is more than one, the last one wins).
//
// The types are:
//	upspin.PathName
//		The Upspin path name of the item being accessed.
//	upspin.UserName
//		The Upspin name of the user attempting the operation.
//	string
//		The operation being performed, usually the method
//		being invoked (Get, Put, etc.)
//	errors.Class
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
			e.Method = arg
		case Class:
			e.Class = arg
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
	if e.Method != "" {
		pad(b, ": ")
		b.WriteString(e.Method)
	}
	if e.Class != 0 {
		pad(b, ": ")
		b.WriteString(e.Class.String())
	}
	if e.Err != nil {
		// Indent on new line if we are cascading upspin errors.
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
