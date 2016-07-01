// +build ignore

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
)

var pat = regexp.MustCompile(`^var\s+([_a-z]+)\s+(bool|int|string)\s+=\s+([0-9a-z]+|"[^"]+")\s+// (.*)`)

func main() {
	var b, all, swtch bytes.Buffer

	// Print the file header.
	fmt.Fprintln(&b, "package flags")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `import "flag"`)
	fmt.Fprintln(&b)

	in, err := os.Open("vars.go")
	if err != nil {
		log.Fatal(err)
	}
	s := bufio.NewScanner(in)
	for s.Scan() {
		fields := pat.FindStringSubmatch(s.Text())
		if fields == nil {
			continue
		}
		name := fields[1]
		typ := fields[2]
		dflt := fields[3]
		usage := fields[4]
		camel := toCamel(name)

		// Declare the function.
		fmt.Fprintf(&b, "// %s returns the value of the -%s flag, defined as:\n", camel, name[1:])
		fmt.Fprintf(&b, "//\t-%s: %s; default %s\n", name[1:], usage, dflt)
		fmt.Fprintf(&b, "func %s() %s { return %s }\n", camel, typ, name)
		fmt.Fprintf(&b, "\n")

		// Add a case to the switch.
		fmt.Fprintf(&swtch, "\t\tcase %q:\n", name[1:])
		fmt.Fprintf(&swtch, "\t\t\tflag.%sVar(&%s, %q, %s, %q)\n", initialCap(typ), name, name[1:], dflt, usage)

		// Add an element to the "all" list.
		fmt.Fprintf(&all, "\t%q,\n", name[1:])
	}
	if s.Err() != nil {
		log.Fatal(s.Err())
	}

	// Print the "all" list.
	fmt.Fprintln(&b, "var all = [...]string{")
	fmt.Fprint(&b, all.String())
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b)

	// Print the Enable function.
	fmt.Fprintln(&b, "// Enable enables the command-line interface for the named flags.")
	fmt.Fprintln(&b, "// If no flags are named, it enables the full set.")
	fmt.Fprintln(&b, "// Enable panics if the flag name is not recognized.")
	fmt.Fprintln(&b, "func Enable(flags ...string) {")
	fmt.Fprintln(&b, "\tif len(flags) == 0 && len(all) != 0 {")
	fmt.Fprintln(&b, "\t\tEnable(all[:]...)")
	fmt.Fprintln(&b, "\t\treturn")
	fmt.Fprintln(&b, "\t}")
	fmt.Fprintln(&b, "\tfor _, f := range flags {")
	fmt.Fprintln(&b, "\t\tswitch f {")
	fmt.Fprint(&b, swtch.String())
	fmt.Fprintln(&b, "\t\tdefault:")
	fmt.Fprintln(&b, "\t\t\tpanic(`flags.Enable: unrecognized flag ` + f)")
	fmt.Fprintln(&b, "\t\t}")
	fmt.Fprintln(&b, "\t}")
	fmt.Fprintln(&b, "}")
	out, err := os.Create("funcs.go")
	if err != nil {
		log.Fatal(err)
	}
	out.Write(b.Bytes())
}

// toCamel converts _a_name to AName.
func toCamel(name string) string {
	var b []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		// Name must be ASCII with lower case, digits and underscores only.
		if !in("abcdefghijklmnopqrstuvwxyz0123456789_", c) {
			log.Fatalf("illegal name %q", name)
		}
		if c != '_' || i == len(name)-1 || !in("abcdefghijklmnopqrstuvwxyz", name[i+1]) {
			b = append(b, c)
			continue
		}
		i++ // Skip '_'
		b = append(b, name[i]-'a'+'A')
	}
	return string(b)
}

func in(set string, c byte) bool {
	return strings.ContainsRune(set, rune(c))
}

func initialCap(s string) string {
	// We know it's ASCII.
	return strings.ToUpper(s[:1]) + s[1:]
}
