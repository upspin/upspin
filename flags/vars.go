// Package flags provides a standard set of command-line flags that may be individually enabled.
// To use the package, call flags.Enable with a comma-separated list of flag names to have available
// on the command line. The empty list enables all flags.
// Call Enable before calling flag.Parse:
//
//	flags.Enable("log_level", "port")
//	flags.Parse()
//
// Flag values are retrieved by calling the function with the camel-cased name:
//
//	log.SetLevel(flags.LogLevel())
//
package flags

//go:generate go run gen.go

// To declare a flag for the package, give its full variable declaration, including the type,
// one per self-contained line, in the style of those listed below.
// The name of the variable should be all lower case, beginning with an underscore.
// Inner underscores are promoted to camel case: _foo_bar becomes the flag foo_bar
// and is available through the public function FooBar.
//
// Run "go generate" to recreate funcs.go, the file that provides the public interface.

var _log_level string = "debug" // the information level for logging

var _port int = 443 // the HTTP serving port
