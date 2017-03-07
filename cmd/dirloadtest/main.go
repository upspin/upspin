package main

import (
	"flag"
	"fmt"
	"math/rand"
	"sync/atomic"

	"upspin.io/bind"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/log"
	"upspin.io/path"
	"upspin.io/upspin"

	_ "upspin.io/transports"
)

var (
	testdir = flag.String("testdir", "testdirserver", "directory under which to conduct tests, relative to the user's root")
	n       = flag.Int("n", 1000, "number of operations")
	j       = flag.Int("j", 3, "number of parallel workers")
)

func main() {
	flag.Parse()

	cfg, err := config.InitConfig(nil)
	if err != nil {
		panic(err)
	}

	dir := upspin.PathName(path.Join(upspin.PathName(cfg.UserName()), *testdir))
	dirserver, err := bind.DirServerFor(cfg, cfg.UserName())
	if err != nil {
		panic(err)
	}

	// Remove all from testdir.
	deleteAll(dirserver, dir)

	err = makePath(dirserver, dir)
	if err != nil {
		panic(err)
	}

	done := make(chan struct{})
	e, err := dirserver.Watch(dir, -1, done)
	if err != nil {
		panic(err)
	}

	wait := make(chan struct{})
	go watch(e, wait)

	workers := make([]chan bool, *j)
	for i := 0; i < *j; i++ {
		workers[i] = make(chan bool)
		go put(dirserver, dir, *n, workers[i])
	}
	for _, w := range workers {
		w <- true
	}
	for _, w := range workers {
		<-w
	}

	// Remove all from testdir.
	log.Printf("Removing testdata...")
	deleteAll(dirserver, dir)

	close(done)
	<-wait
}

func watch(event <-chan upspin.Event, wait chan struct{}) {
	i := 0
	for {
		e, ok := <-event
		if !ok {
			log.Printf("Server closed the events channel.")
			break
		}
		if e.Error != nil {
			log.Error.Print(e.Error)
		}
		if rand.Intn(100) == 0 {
			log.Printf("Got event: %v", e)
		}
		i++
	}
	log.Printf("Got %d events", i)
	close(wait)
}

func put(dirserver upspin.DirServer, dir upspin.PathName, n int, worker chan bool) {
	<-worker // Wait for confirmation that we can go ahead.
	for i := 0; i < n; i++ {
		err := mkdir(dirserver, dir+"/"+mkName())
		if err != nil {
			log.Error.Print(err)
		}
	}
	close(worker)
}

func makePath(dir upspin.DirServer, name upspin.PathName) error {
	p, err := path.Parse(name)
	if err != nil {
		return err
	}
	for i := 0; i < p.NElem(); i++ {
		err = mkdirIfNotExist(dir, p.First(i+1).Path())
		if err != nil {
			return err
		}
	}
	return nil
}

func mkdir(dir upspin.DirServer, name upspin.PathName) error {
	entry := &upspin.DirEntry{
		Name:       name,
		SignedName: name,
		Attr:       upspin.AttrDirectory,
	}
	_, err := dir.Put(entry)
	return err
}

func mkdirIfNotExist(dir upspin.DirServer, name upspin.PathName) error {
	err := mkdir(dir, name)
	if err == nil {
		return nil
	}
	if errors.Match(errors.E(errors.Exist), err) {
		return nil
	}
	return err
}

// deleteAll recursively deletes the directory named by path through the
// provided DirServer, first deleting path/Access and then path/*.
func deleteAll(dir upspin.DirServer, path upspin.PathName) error {
	entries, err := dir.Glob(string(path + "/*"))
	if err != nil && err != upspin.ErrFollowLink {
		return err
	}
	for _, e := range entries {
		if _, err := dir.Delete(e.Name); err != nil {
			return err
		}
	}
	return nil
}

var nameCount int64

func mkName() upspin.PathName {
	name := atomic.AddInt64(&nameCount, 1)
	return upspin.PathName(fmt.Sprintf("%d", name))
}
