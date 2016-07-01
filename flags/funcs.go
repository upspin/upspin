package flags

import "flag"

// LogLevel returns the value of the -log_level flag, defined as:
//	-log_level: the information level for logging; default "debug"
func LogLevel() string { return _log_level }

// Port returns the value of the -port flag, defined as:
//	-port: the HTTP serving port; default 443
func Port() int { return _port }

var all = [...]string{
	"log_level",
	"port",
}

// Enable enables the command-line interface for the named flags.
// If no flags are named, it enables the full set.
// Enable panics if the flag name is not recognized.
func Enable(flags ...string) {
	if len(flags) == 0 && len(all) != 0 {
		Enable(all[:]...)
		return
	}
	for _, f := range flags {
		switch f {
		case "log_level":
			flag.StringVar(&_log_level, "log_level", "debug", "the information level for logging")
		case "port":
			flag.IntVar(&_port, "port", 443, "the HTTP serving port")
		default:
			panic(`flags.Enable: unrecognized flag ` + f)
		}
	}
}
