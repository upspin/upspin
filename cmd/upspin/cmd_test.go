// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"upspin.io/upbox"
	"upspin.io/upspin"
)

var allCmdTests = []*[]cmdTest{
	&basicCmdTests,
	&cpTests,
	&globTests,
	&keygenTests,
	&lsTests,
	&shareTests,
	&suffixedUserTests,
}

// TestCommands runs the tests defined in cmdTests as subtests.
func TestCommands(t *testing.T) {
	// Set up upbox.
	schema, err := upbox.SchemaFromYAML(upboxSchema)
	if err != nil {
		t.Fatalf("setting up schema: %v", err)
	}
	err = schema.Start()
	if err != nil {
		t.Fatalf("starting schema: %v", err)
	}

	// Loop over the tests in sequence, building state as we go.
	for _, testSuite := range allCmdTests {
		for _, test := range *testSuite {
			// We create a runner for each cmdTest so the Config and State
			// are constructed from the environment each time.
			r := &runner{
				fs:     flag.NewFlagSet(test.name, flag.PanicOnError), // panic if there's trouble.
				schema: schema,
			}
			state, _, ok := setup(r.fs, []string{"-config=" + r.config(test.user), "test"})
			if !ok {
				t.Fatal("setup failed; bad arg list?")
			}
			r.state = state
			t.Run(test.name, r.run(&test))
		}
	}

	// Tear down upbox.
	schema.Stop()
}

// TODO: Loop over server implementations?

const upboxSchema = `
users:
  - name: ann@example.com
  - name: chris@example.com
  - name: kelly@example.com
  - name: lee@example.com
servers:
  - name: keyserver
  - name: storeserver
  - name: dirserver
    flags:
      kind: server
domain: example.com
`

const (
	ann   = upspin.UserName("ann@example.com")
	chris = upspin.UserName("chris@example.com")
	kelly = upspin.UserName("kelly@example.com")
	lee   = upspin.UserName("lee@example.com")
)

// devNull gives EOF on read and absorbs anything error-free on write, like Unix's /dev/null.
type devNull struct{}

func (devNull) Write(b []byte) (int, error) { return len(b), nil }
func (devNull) Read([]byte) (int, error)    { return 0, io.EOF }
func (devNull) Close() error                { return nil }

// runner controls the execution of a sequence of cmdTests.
// It holds state, including the running upbox instance, and
// as the cmdTests are run the state of the upbox and its servers
// are modified and available to subsequent subcommands.
// It's a little bit like the upspin shell command, but through
// upbox can start the test services and provides mechanisms
// to valid results and test state.
type runner struct {
	// fs, not flag.CommandLine, holds the flags for the upspin state.
	fs *flag.FlagSet
	// state is the internal state of the upspin command.
	state *State
	// schema holds the running upbox instance.
	schema *upbox.Schema
	// failed is set to true when a command fails; following subcommands are ignored.
	// It is reset before the next cmdTest runs.
	failed bool
}

// runOne runs a single subcommand.
func (r *runner) runOne(t *testing.T, cmdLine string) {
	if r.failed {
		return
	}
	// If the command calls Exit or Exitf, that will panic.
	// It may be benign; if not, the reason is in standard error.
	// We catch the panic here, which is sufficient to capture the error output.
	defer func() {
		rec := recover()
		switch problem := rec.(type) {
		case nil:
		case string:
			if problem == "exit" {
				// OK; this was a subcommand calling exit
				return
			}
			r.failed = true
			t.Errorf("%v", problem)
		default:
			t.Errorf("%v", problem)
		}
	}()
	r.state.run(strings.Fields(cmdLine))
}

// run runs all the subcommands in cmd.
func (r *runner) run(cmd *cmdTest) func(t *testing.T) {
	return func(t *testing.T) {
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		var stdin io.ReadCloser = devNull{}
		if cmd.stdin != "" {
			stdin = ioutil.NopCloser(strings.NewReader(cmd.stdin))
		}
		r.state.SetIO(stdin, stdout, stderr)
		defer r.state.DefaultIO()
		r.state.Interactive = true // So we can regain control after an error.
		for _, cmdLine := range cmd.cmds {
			r.runOne(t, cmdLine)
		}
		cmd.post(t, r, cmd, stdout.String(), stderr.String())
	}
}

// config returns the file name of the config file for the given user.
func (r *runner) config(userName upspin.UserName) string {
	return r.schema.Config(string(userName))
}

// expect is a post function that verifies that standard output from the
// command contains all the words, in order.
func expect(words ...string) func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		if stderr != "" {
			t.Fatalf("%q: unexpected error:\n\t%q", cmd.name, stderr)
		}
		// Stdout should contain all words, in order, non-abutting.
		out := stdout
		prev := "beginning"
		for _, word := range words {
			index := strings.Index(out, word)
			if index < 0 {
				t.Fatalf("%q: output did not contain %q after %q. output:\n%s", cmd.name, word, prev, stdout)
				return
			}
			prev = word
			out = out[index:]
		}
	}
}

// expectNoOutput is a post function that verifies that standard output from the
// command is empty.
func expectNoOutput() func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		if stderr != "" {
			t.Fatalf("%q: unexpected error:\n\t%q", cmd.name, stderr)
		}
		if stdout != "" {
			t.Fatalf("%q: unexpected output:\n\t%q", cmd.name, stdout)
		}
	}
}

// fail is a post function that verifies that standard error contains the text of errStr.
func fail(errStr string) func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		if stderr == "" {
			t.Fatalf("%q: expected error, got none", cmd.name)
		}
		if !strings.Contains(stderr, errStr) {
			t.Fatalf("%q: unexpected error (expected %q)\n\t%q", cmd.name, errStr, stderr)
		}
	}
}

// dump is a post function that just prints the stdout and stderr.
// If Continue is false, dump calls t.Fatal.
// The function is handy when debugging cmdTest scripts.
func dump(Continue bool) func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		t.Errorf("Stdout:\n%s\n", stdout)
		t.Errorf("Stderr:\n%s\n", stderr)
		if !Continue {
			t.Fatal("dump stops test")
		}
	}
}

// do is just a shorthand to make the cmdTests format more neatly.
func do(s ...string) []string {
	return s
}

// putFile is a cmdTest to add the named file with the given contents and
// check that it is created.
func putFile(user upspin.UserName, name, contents string) cmdTest {
	return cmdTest{
		name: fmt.Sprintf("add %s", name),
		user: user,
		cmds: do(
			"put "+name,
			"get "+name,
		),
		stdin: contents,
		post:  expect(contents),
	}
}

// testTempDir creates, if not already present, a temporary directory
// with basename dir. It panics if it does not exist and cannot be created.
func testTempDir(dir string, keepOld bool) string {
	dir = filepath.Join(os.TempDir(), dir)
	if !keepOld {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}
	err := os.Mkdir(dir, 0700)
	if err != nil && !os.IsExist(err) {
		panic(err)
	}
	return dir
}

// testTempGlob calls testTempDir(dir, keepOld) and returns
// its name appended with "/*".
func testTempGlob(dir string) string {
	return filepath.Join(testTempDir(dir, keepOld), "*")
}

// keygenVerify is a post function for keygen itself.
// It verifies that the keys were created correctly,
// and removes the directory if persist is false.
func keygenVerify(dir, public, secret, secret2 string, persist bool) func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		t.Log("stdout:", stdout)
		t.Log("stderr:", stdout)
		keyVerify(t, filepath.Join(dir, "public.upspinkey"), public)
		keyVerify(t, filepath.Join(dir, "secret.upspinkey"), secret)
		if secret2 != "" {
			keyVerify(t, filepath.Join(dir, "secret2.upspinkey"), secret2)
		}
		if !persist {
			os.RemoveAll(dir)
		}
	}
}

func keyVerify(t *testing.T, name, prefix string) {
	key, err := ioutil.ReadFile(name)
	if err != nil {
		t.Errorf("cannot read key %q: %v", name, err)
	}
	if !strings.Contains(string(key), prefix) {
		if len(key) > 16 {
			key = key[:16]
		}
		t.Errorf("invalid key: got %q...; expected %q...", key, prefix)
	}
}

// suffixedUserExists is a post function. It returns a function that ensures that a
// config file and key files exist for the suffixed user.
func suffixedUserExists(user, suffix string) func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
	return func(t *testing.T, r *runner, cmd *cmdTest, stdout, stderr string) {
		// Both config files should exist.
		cfgFile := r.config(upspin.UserName(user + "@example.com"))
		if _, err := os.Stat(cfgFile); err != nil {
			t.Fatalf("%s", err)
		}
		suser := fmt.Sprintf("%s+%s@example.com", user, suffix)
		scfgFile := cfgFile + "." + suffix
		if _, err := os.Stat(scfgFile); err != nil {
			t.Fatalf("%s: %s", suser, err)
		}

		// Key files should exist.
		secretsDir := testTempDir("key", keepOld)
		if _, err := os.Stat(filepath.Join(secretsDir, "public.upspinkey")); err != nil {
			t.Fatalf("%s: %s", suser, err)
		}
		if _, err := os.Stat(filepath.Join(secretsDir, "secret.upspinkey")); err != nil {
			t.Fatalf("%s: %s", suser, err)
		}
	}
}
